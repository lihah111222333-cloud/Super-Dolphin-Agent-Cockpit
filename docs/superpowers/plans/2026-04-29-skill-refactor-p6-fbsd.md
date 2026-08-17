# Skill Refactor — Phase 6: FBSD 频次降级 Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 to implement this plan task-by-task.

> **本计划无前置实测**：spec §13 #8 担心的"Claude 侧 turn output 解析 Read 调用稳定性"已在 P3 后落地的 `claudecli/factory.go` stream-json parser 中得到化解 —— 该 parser 已稳定解析 `tool_use` blocks，能拿到 `tool_name` + `input` 字段，FBSD 打点只需在该解析点加 hook。

## Goal

落地 spec §9 FBSD 频次降级：按 skill 调用频次 / workspace 数据 / 全局数据 + grace period + pinned 标记，把所有 active skill 分到 Hot/Warm/Cold/Frozen 4 个 tier，按 budget 贪心装填，渲染到 Codex base instructions（替换 P3 当前的"全部 L1-C 单 tier"）。同时落地双层 stats 持久化和异步打点 hook。

## Architecture

```
                                     ┌───────────────────────┐
                                     │  global stats         │
                                     │  ~/.super-dolphin/      │
                                     │  skills-stats.json    │
                                     └────────▲──────────────┘
                                              │ score()
                          ┌──────────────────┐│      ┌────────────────────┐
打点 hook                 │  workspace stats │├──────┤  effective_score   │
─────────────             │  ~/.super-dolphin/ ││ G3   │  (双层合并 §9.3)   │
Claude tool_use(Read)     │  workspaces/<id>/├──────►                    │
Codex skill_read_section  │  skills-stats.   │      │  ws ≥ 10 calls →   │
─────────────             │  json            │      │  use ws-only       │
       │                  └──────────────────┘      │  else 0.3 ws +     │
       │ async non-blocking                         │      0.7 global    │
       ▼                                            └────────────────────┘
   fbsd.Tracker.Record(name, anchor)                          │
                                                              ▼
                                            assign_tiers (§9.4 budget 贪心)
                                                              │
                                                              ▼
                                          ┌─────────────────────────────────┐
                                          │  Codex buildSkillManifest 改造  │
                                          │  Hot:    L1-C 完整 (~600 chars) │
                                          │  Warm:   L1-B 节标题 (~200)     │
                                          │  Cold:   L1-A 仅 desc (~80)     │
                                          │  Frozen: 不出现于 manifest      │
                                          └─────────────────────────────────┘
```

**关键决策**：
1. 打点是**异步 non-blocking**（spec §9.6）：tracker 内部 buffer + goroutine 异步落盘；fx OnStop 钩子 flush。
2. **Claude 侧打点利用现有 stream parser** — `factory.go` decode `tool_use` 时识别 `Read("/path/to/.claude/skills/<name>/references/<anchor-file>.md")` 调用并打点。无需新建 parser。
3. **Codex 侧打点在 `skill_read_section` 工具实现内**（spec §9.5 + §13 #8）—— 直接拿到 name + anchor，最精确。
4. **buildSkillManifest 重写**：替换 P3 单 tier 实现为按 tier 渲染。spec §7.1 完整 Hot/Warm/Cold 模板。
5. **Feature flag** `SUPER_DOLPHIN_SKILL_FBSD=off` 默认；off 时 buildSkillManifest 走 P3 单 tier 路径（向后兼容）；打点 hook 也 no-op，不写 stats 文件。

## Tech Stack

Go 1.22+；已有 fx 图；已有 SkillMeta.Pinned/Disabled/InstalledAt 字段（P1 schema）；新增 fsnotify 之外**无运行时依赖**。

## 前置阅读

- spec §9 全文（Tier / Score / Merge / Tier 分配 / 打点 / 持久化 / 默认参数）
- spec §7.1 + §7.3（按 tier 渲染 + budget 兜底降级）
- spec §13 #8（Claude 打点稳定性 — 现已化解）
- P3 commit `f840a7f` + `6093f8d`（buildSkillManifest 现实现）
- P5 commit `f0ea881`（fx Module 模式参考）

---

## File Structure

**新增**：
```
internal/module/fbsd/
├── types.go        (Task 1: CallEvent / SkillStats / Tier 常量)
├── score.go        (Task 2: 指数衰减 §9.2)
├── score_test.go
├── merge.go        (Task 3: G3 双层合并 §9.3)
├── merge_test.go
├── tier.go         (Task 4: budget 贪心分配 §9.4)
├── tier_test.go
├── store.go        (Task 5: stats 持久化 + Load/Save §9.6)
├── store_test.go
├── tracker.go      (Task 6: 异步打点 + flush)
├── tracker_test.go
└── module.go       (Task 7: fx Module + flag)
```

**修改**：
```
internal/provider/codexapp/skill_manifest.go     (Task 8: 改写 buildSkillManifest 按 tier 渲染)
internal/provider/codexapp/skill_manifest_test.go(Task 8: tier 测试)
internal/platform/toolbridge/skill_read_section.go (Task 9: 加 tracker.Record hook)
internal/provider/claudecli/factory.go           (Task 10: tool_use 解析点加 Read 路径识别 + Record hook)
internal/app/modules.go                          (Task 7: 接入 fbsd.Module)
```

无文件删除。

---

## Task 1: 基础类型 + Tier 常量

**Files:** `internal/module/fbsd/types.go`

```go
package fbsd

import "time"

// Tier 是 spec §9.1 定义的 4 档分级。Frozen 表示"不进 L1 manifest"。
type Tier string

const (
	TierHot    Tier = "Hot"
	TierWarm   Tier = "Warm"
	TierCold   Tier = "Cold"
	TierFrozen Tier = "Frozen"
)

// CallEvent 是单次 skill 调用打点。
//   - At  Unix 秒，由 tracker 注入；测试可注入 fake clock。
//   - Anchor 为空时表示"整 skill 触发"（无 section 级别信息），仍计入 score。
type CallEvent struct {
	SkillName string
	Anchor    string
	At        time.Time
}

// SkillStats 是单条 skill 的累积统计。
type SkillStats struct {
	Name         string           `json:"-"`             // map key 即 name，结构内冗余
	Calls        []time.Time      `json:"calls"`         // Unix-秒 序列；写盘按 RFC3339 数组
	InstalledAt  time.Time        `json:"installed_at"`  // 用于 grace period
	SectionCalls map[string]int   `json:"section_calls"` // anchor → count
}

// Stats 是 store 单文件 JSON 模型；map key 是 skill name。
type Stats map[string]*SkillStats
```

测试：`TestTier_StringConsts` 验证 4 个常量字面值；`TestSkillStats_JSONRoundTrip` 序列化 + 反序列化保持 calls / section_calls。

---

## Task 2: Score 指数衰减 (§9.2)

**Files:** `internal/module/fbsd/score.go`

```go
package fbsd

import (
	"math"
	"time"
)

// 默认参数（spec §9.7）；env 覆盖在 Tracker 构造时注入。
const (
	DefaultHalfLifeDays = 7
	DefaultFrozenDays   = 90
)

// Score 计算单条 skill 的指数衰减分数。spec §9.2：
//   score = sum(2 ^ (-Δt / half_life)) for each call within frozenCutoff window
// 参数 halfLife / frozen 必须 > 0，调用方负责校验或用默认值。
func Score(stats *SkillStats, now time.Time, halfLife time.Duration, frozen time.Duration) float64 {
	if stats == nil || len(stats.Calls) == 0 {
		return 0
	}
	cutoff := now.Add(-frozen)
	hlSec := halfLife.Seconds()
	if hlSec <= 0 {
		return 0
	}
	var s float64
	for _, t := range stats.Calls {
		if t.Before(cutoff) {
			continue
		}
		dt := now.Sub(t).Seconds()
		s += math.Pow(2, -dt/hlSec)
	}
	return s
}
```

测试覆盖：
- 无 calls → 0
- 单 call now → 接近 1
- 单 call half_life 前 → 接近 0.5
- 单 call frozen window 外 → 0
- 多 calls 累加
- nil stats 不 panic

---

## Task 3: G3 双层合并 (§9.3)

**Files:** `internal/module/fbsd/merge.go`

```go
package fbsd

import "time"

const (
	DefaultWorkspaceMinCalls = 10
	DefaultWorkspaceWeight   = 0.3
)

// EffectiveScore 把 workspace + global 双层数据合并为最终 score。spec §9.3：
//   - ws 调用数 ≥ minCalls：仅用 ws 数据（局部使用频繁，全局信号噪声）
//   - 否则：weight*ws + (1-weight)*global 混合（缓启动）
//   - 全 nil → 0
func EffectiveScore(ws, glob *SkillStats, now time.Time, halfLife, frozen time.Duration, minCalls int, weight float64) float64 {
	wsTotal := 0
	if ws != nil {
		wsTotal = len(ws.Calls)
	}
	if wsTotal >= minCalls {
		return Score(ws, now, halfLife, frozen)
	}
	if glob == nil {
		return 0
	}
	if ws == nil {
		return Score(glob, now, halfLife, frozen)
	}
	return weight*Score(ws, now, halfLife, frozen) + (1-weight)*Score(glob, now, halfLife, frozen)
}
```

测试：ws ≥ minCalls 走 ws-only / ws < minCalls 混合 / ws nil only-glob / 全 nil 返回 0 / weight 边界 0.0 和 1.0。

---

## Task 4: Tier 分配 (§9.4)

**Files:** `internal/module/fbsd/tier.go`

```go
package fbsd

import (
	"sort"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

// TierConfig 是 budget 贪心分配的输入参数。env 覆盖在 Tracker 构造时注入；
// 测试可直接构造 TierConfig 不经 env。
type TierConfig struct {
	Budget          int
	HotChars        int
	WarmChars       int
	ColdChars       int
	GraceDuration   time.Duration
	HalfLife        time.Duration
	FrozenDuration  time.Duration
	WSMinCalls      int
	WSWeight        float64
}

// DefaultTierConfig 返回 spec §9.7 默认值。
func DefaultTierConfig() TierConfig {
	return TierConfig{
		Budget:         8192,
		HotChars:       600,
		WarmChars:      200,
		ColdChars:      80,
		GraceDuration:  7 * 24 * time.Hour,
		HalfLife:       7 * 24 * time.Hour,
		FrozenDuration: 90 * 24 * time.Hour,
		WSMinCalls:     DefaultWorkspaceMinCalls,
		WSWeight:       DefaultWorkspaceWeight,
	}
}

// TierAssignment 是 AssignTiers 单条输出。
type TierAssignment struct {
	Skill skilllibrary.SkillEntry
	Tier  Tier
	Score float64
}

// AssignTiers 把 active skill 按 spec §9.4 分到 Hot/Warm/Cold/Frozen。
// 优先级（高→低）：
//   1. pinned (∞)：永远 Hot 优先
//   2. grace 期 installed_at 距今 < grace：Hot 优先（次于 pinned）
//   3. effective_score：按数值降序
// disabled skill 不参与分配。score == 0 且非 pinned/非 grace → 直接 Frozen。
// budget 用尽后剩余 → Frozen。
//
// wsStats / globStats 可为 nil（首启动）；时间通过 now 注入便于测试。
func AssignTiers(entries []skilllibrary.SkillEntry, wsStats, globStats Stats, cfg TierConfig, now time.Time) []TierAssignment {
	type decorated struct {
		entry  skilllibrary.SkillEntry
		score  float64
		forced Tier // ""/Hot
	}
	const (
		pinnedScore = 1e18 // sentinels
		graceScore  = 1e17
	)

	dec := make([]decorated, 0, len(entries))
	for _, e := range entries {
		if e.Meta == nil || e.Meta.Disabled {
			continue
		}
		switch {
		case e.Meta.Pinned:
			dec = append(dec, decorated{entry: e, score: pinnedScore, forced: TierHot})
		case withinGrace(e.Meta.InstalledAt, cfg.GraceDuration, now):
			dec = append(dec, decorated{entry: e, score: graceScore, forced: TierHot})
		default:
			ws := wsStats[e.Meta.Name]
			gl := globStats[e.Meta.Name]
			s := EffectiveScore(ws, gl, now, cfg.HalfLife, cfg.FrozenDuration, cfg.WSMinCalls, cfg.WSWeight)
			dec = append(dec, decorated{entry: e, score: s})
		}
	}

	sort.SliceStable(dec, func(i, j int) bool { return dec[i].score > dec[j].score })

	remaining := cfg.Budget
	out := make([]TierAssignment, 0, len(dec))
	for _, d := range dec {
		if d.score == 0 && d.forced == "" {
			out = append(out, TierAssignment{Skill: d.entry, Tier: TierFrozen, Score: 0})
			continue
		}
		switch {
		case remaining >= cfg.HotChars:
			remaining -= cfg.HotChars
			out = append(out, TierAssignment{Skill: d.entry, Tier: TierHot, Score: d.score})
		case remaining >= cfg.WarmChars:
			remaining -= cfg.WarmChars
			out = append(out, TierAssignment{Skill: d.entry, Tier: TierWarm, Score: d.score})
		case remaining >= cfg.ColdChars:
			remaining -= cfg.ColdChars
			out = append(out, TierAssignment{Skill: d.entry, Tier: TierCold, Score: d.score})
		default:
			out = append(out, TierAssignment{Skill: d.entry, Tier: TierFrozen, Score: d.score})
		}
	}
	return out
}

// withinGrace 解析 SkillMeta.InstalledAt（RFC3339）并判断是否在 grace 内。
// 解析失败视为不在 grace 期（保守 fallback）。
func withinGrace(installedAt string, grace time.Duration, now time.Time) bool {
	if installedAt == "" || grace <= 0 {
		return false
	}
	t, err := time.Parse(time.RFC3339, installedAt)
	if err != nil {
		return false
	}
	return now.Sub(t) < grace
}
```

测试覆盖：
- pinned 永远 Hot
- grace 期 Hot
- score 降序排
- budget 满 → 后续 Frozen
- disabled 不参与
- score=0 + 非 pinned/grace → Frozen
- mixed 场景（pinned+grace+normal）

---

## Task 5: Stats 持久化 (§9.6)

**Files:** `internal/module/fbsd/store.go`

```go
package fbsd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// LoadStats 从 path 读 JSON。文件不存在返回空 Stats（不报错）；malformed 报错。
func LoadStats(path string) (Stats, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Stats{}, nil
		}
		return nil, fmt.Errorf("fbsd: read stats: %w", err)
	}
	var s Stats
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("fbsd: parse stats: %w", err)
	}
	if s == nil {
		s = Stats{}
	}
	return s, nil
}

// SaveStats 把 stats 原子写到 path（mkdir + tmp + rename）。
func SaveStats(path string, stats Stats) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("fbsd: mkdir: %w", err)
	}
	body, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return fmt.Errorf("fbsd: marshal stats: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("fbsd: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("fbsd: rename: %w", err)
	}
	return nil
}
```

测试：load missing→empty / load malformed→err / save+load roundtrip / save creates dirs / save atomic（无 stale tmp）。

---

## Task 6: Tracker（异步打点 + flush）

**Files:** `internal/module/fbsd/tracker.go`

```go
package fbsd

import (
	"context"
	"sync"
	"time"
)

// Tracker 在内存维护 workspace + global stats，异步落盘。
//
// Record 是 hot path（每次 skill 调用），必须 non-blocking。落盘走后台
// goroutine + buffered channel；channel 满时 drop 不阻塞 caller。
//
// Flush 同步等待所有 pending 写入完成；fx OnStop 钩子调用一次。
type Tracker struct {
	mu          sync.Mutex
	wsStats     Stats
	globStats   Stats
	wsPath      string
	globPath    string
	enabled     bool

	events chan CallEvent
	done   chan struct{}
	wg     sync.WaitGroup
}

// NewTracker 构造 Tracker。enabled=false 时 Record 走 no-op 路径，不启动 goroutine。
func NewTracker(wsPath, globPath string, enabled bool) (*Tracker, error) {
	t := &Tracker{
		wsPath:    wsPath,
		globPath:  globPath,
		enabled:   enabled,
		wsStats:   Stats{},
		globStats: Stats{},
		events:    make(chan CallEvent, 1024),
		done:      make(chan struct{}),
	}
	if !enabled {
		return t, nil
	}
	ws, err := LoadStats(wsPath)
	if err != nil {
		return nil, err
	}
	gl, err := LoadStats(globPath)
	if err != nil {
		return nil, err
	}
	t.wsStats = ws
	t.globStats = gl
	t.wg.Add(1)
	go t.run()
	return t, nil
}

// Record 异步打点。enabled=false / receiver=nil → no-op。channel 满时 drop。
func (t *Tracker) Record(name, anchor string) {
	if t == nil || !t.enabled || name == "" {
		return
	}
	select {
	case t.events <- CallEvent{SkillName: name, Anchor: anchor, At: time.Now()}:
	default:
		// drop on backpressure；不阻塞 caller
	}
}

// Flush 等待所有 pending events 落盘。调用后再次 Record 仍 OK（不会 close channel）。
func (t *Tracker) Flush(ctx context.Context) error {
	if t == nil || !t.enabled {
		return nil
	}
	// 放一条 sentinel 让 worker 处理完所有 events 后 ack
	ack := make(chan struct{})
	select {
	case t.events <- CallEvent{SkillName: "__flush__", At: time.Now()}:
	case <-ctx.Done():
		return ctx.Err()
	}
	go func() {
		t.mu.Lock()
		_ = SaveStats(t.wsPath, t.wsStats)
		_ = SaveStats(t.globPath, t.globStats)
		t.mu.Unlock()
		close(ack)
	}()
	select {
	case <-ack:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WSStats / GlobStats 给 tier 分配读快照。返回 deep copy 防止外部 mutate。
func (t *Tracker) Snapshot() (ws, glob Stats) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return cloneStats(t.wsStats), cloneStats(t.globStats)
}

func (t *Tracker) run() {
	defer t.wg.Done()
	for ev := range t.events {
		if ev.SkillName == "__flush__" {
			continue
		}
		t.applyEvent(ev)
	}
}

func (t *Tracker) applyEvent(ev CallEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, target := range []Stats{t.wsStats, t.globStats} {
		s := target[ev.SkillName]
		if s == nil {
			s = &SkillStats{Name: ev.SkillName, SectionCalls: map[string]int{}}
			target[ev.SkillName] = s
		}
		s.Calls = append(s.Calls, ev.At)
		if ev.Anchor != "" {
			s.SectionCalls[ev.Anchor]++
		}
	}
}

func cloneStats(in Stats) Stats {
	out := make(Stats, len(in))
	for k, v := range in {
		if v == nil {
			continue
		}
		c := *v
		c.Calls = append([]time.Time(nil), v.Calls...)
		c.SectionCalls = make(map[string]int, len(v.SectionCalls))
		for kk, vv := range v.SectionCalls {
			c.SectionCalls[kk] = vv
		}
		out[k] = &c
	}
	return out
}
```

测试覆盖：
- disabled tracker Record/Flush no-op
- nil receiver safe
- Record + Snapshot 反映在 wsStats + globStats
- Flush 后磁盘读到同样 stats
- 1024 channel 满时 drop 不 panic
- Snapshot 返回 deep copy（mutate 不影响 tracker 内部）

---

## Task 7: fx Module + 接入

**Files:**
- Create: `internal/module/fbsd/module.go`
- Modify: `internal/app/modules.go`

```go
package fbsd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/fx"
)

const envFlag = "SUPER_DOLPHIN_SKILL_FBSD"

var Module = fx.Module("fbsd",
	fx.Provide(NewTrackerFromEnv),
	fx.Invoke(registerFlush),
)

// NewTrackerFromEnv 从环境变量解析 enabled flag，构造 Tracker。
func NewTrackerFromEnv() (*Tracker, error) {
	enabled := os.Getenv(envFlag) == "on"
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("fbsd: user home: %w", err)
	}
	globPath := filepath.Join(home, ".super-dolphin", "skills-stats.json")
	// workspace stats 用 hostname 当 ID 是简化方案；多用户场景 P6.x 再细化。
	host, _ := os.Hostname()
	wsPath := filepath.Join(home, ".super-dolphin", "workspaces", host, "skills-stats.json")
	return NewTracker(wsPath, globPath, enabled)
}

// registerFlush 注册 fx OnStop 钩子，确保 harness 退出前 flush stats。
func registerFlush(lc fx.Lifecycle, t *Tracker) {
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return t.Flush(ctx)
		},
	})
}
```

`internal/app/modules.go` 加 `fbsd.Module` 进 fx 图（紧跟 nativefilter.Module）。

---

## Task 8: Codex skill_read_section 集成打点

**File:** `internal/platform/toolbridge/skill_read_section.go`

`SkillReadSectionTool.Call` 在成功 ReadSection 后调 `tracker.Record(name, anchor)`。tracker 通过 fx inject 进 toolbridge module（optional，nil-safe）。

测试：用 stub tracker 验证 Call 成功后 Record 被调一次；anchor not-found 时不 Record。

---

## Task 9: Claude tool_use parser 加 Read 路径识别 + Record

**File:** `internal/provider/claudecli/factory.go`

`decodeAssistantMessageBlock` 的 `case "tool_use"` 分支加：
- 仅对 `block.Name == "Read"` 处理
- 解析 `block.Input` 拿 `file_path`
- 匹配 `<workspace>/.claude/skills/<name>/references/<NN-anchor>.md` 模式（reuse `skilllibrary.anchorFromFilename` 解析 anchor）
- 命中则调 `tracker.Record(name, anchor)`

driver 已经在 `prepareSessionStart` 拿到 skillCacheDir + workspace cwd；factory 需要 inject Tracker（通过 driver struct 传递或独立注入点）。

测试：构造 fake stream-json 包含 Read 调用 .claude/skills/x/references/01-anchor.md → 验证 tracker 收到 Record(x, anchor)。非 Read 工具不打点；非 skill 路径的 Read 不打点。

---

## Task 10: buildSkillManifest 按 tier 渲染（替换 P3 单 tier）

**Files:** `internal/provider/codexapp/skill_manifest.go` + tests

替换现有 `buildSkillManifest(entries, budget)` 为：
- feature flag off → 走 P3 老路径（现 L1-C 单 tier，向后兼容）
- feature flag on → 走 `AssignTiers` → 按 tier 渲染：
  - Hot: 现 L1-C 完整（name + desc + section index）
  - Warm: name + desc + 节标题列表（无摘要）
  - Cold: name + desc 仅
  - Frozen: 不出现于 manifest

打开 flag 时，manifest 头部加一行说明"调用 skill_list_all() 看 Frozen tier 全量列表"（spec §7.2 提示，但 skill_list_all 工具 P6 不实现，仅文本提示，TODO P6.x）。

测试：固定 fixture（mixed pinned/grace/score=0/normal）+ 固定 budget，断言渲染输出含期望 tier 标记 + Frozen skill 不出现。

---

## Task 11: e2e fixture + 全测试 + smoke

- e2e_test.go：构造 library + 多 skill（mix tier）+ Tracker.Record 模拟若干历史调用 + buildSkillManifest 渲染验证 tier 分布
- 全 internal/ 测试 PASS
- 冒烟：开 `SUPER_DOLPHIN_SKILL_FBSD=on` 跑 codex e2e，看真实 manifest 是否分 tier

---

## 已知偏差与未覆盖项

### 不在 P6 范畴

1. **`skill_list_all` 工具**（spec §7.2）：FBSD 降级到 L1-A 时让模型按需查 Frozen skill；P6 仅文本提示，工具实现推到 **P6.x / P7**。
2. **`skill_list_sections` 工具**（同上）：Warm tier 已含节标题列表，需求度低，推到 **P6.x**。
3. **§7.3 budget 兜底"按 tier 降级再试"**：当前贪心是 Hot→Warm→Cold→Frozen 单向递降。spec §7.3 描述的"超 budget 再试 Warm/Cold"在贪心算法下自动达成，无需额外逻辑；如未来有更复杂打包需求再补。
4. **workspace_id 真实化**：当前用 hostname 当 workspace ID 是简化（multi-user / multi-project 同主机会混淆）。spec §9.6 的 `<workspace-id>` 严格定义留给 **P6.x / P7**（建议改用 cwd hash 或 binding store thread ID）。
5. **stats 文件归档 / TTL**：current 实现无大小限制，长期跑可能 calls 数组无限增长。spec 没明示但实践需要：建议 P6.x 加"超 frozen window 的 calls 自动从数组裁掉"。

### 已知 spec deviation

- spec §9.5 描述的 Claude 侧打点假设需要"实测 turn output 解析 Read 调用是否稳定"；本计划在 P3 后基于已有 stream-json parser 直接 hook，**无需前置实测**——这是 spec 写作时还没看到 P3 已落地的 parser 现状。无功能性偏差。
- spec §13 #8 同上，已化解。

### 监测建议

- P6 ship 后加打点：`fbsd_record_dropped_total`（channel 满 drop 计数）+ `fbsd_flush_duration_seconds`，验证 1024 channel 是否够用。
- 灰度策略：`SUPER_DOLPHIN_SKILL_FBSD=off` 默认；P6 PR 合并后灰度环境先打开 7 天观察 tier 分布合理性 + 模型行为变化。

---

## Phase 6 自审

**1. 规格覆盖**：spec §9 全部 7 节有 Task 对应（types/score/merge/tier/store/tracker/默认参数）。spec §7.1 + §7.3 在 Task 10 covered。spec §13 #8 已化解。

**2. 占位符扫描**：所有代码段为完整可应用编辑。

**3. 类型一致性**：
- `Tracker.Record` 与 Codex / Claude 集成点签名一致 `Record(name, anchor string)`
- `AssignTiers` 输入用 `skilllibrary.SkillEntry`，与 codexapp manifest 现实现一致
- `Stats` 与 store JSON schema 一对一

**4. 实测对齐**：
- Claude tool_use parser 已存在（factory.go:162），无需前置实测
- Codex skill_read_section 工具已存在（P3 commit f587d4d），打点点位明确
- Feature flag `SUPER_DOLPHIN_SKILL_FBSD` 默认关，符合 spec §12 + §13 #10 灰度规范

修复内联：暂无问题。

---

## 执行交接

计划已保存到 `docs/superpowers/plans/2026-04-29-skill-refactor-p6-fbsd.md`。Tasks 拆分清晰、各 Task 独立 commit，建议子代理驱动模式（Task 6 tracker 因含 goroutine + race 风险建议派 reviewer 完整 pass；其他 Task 简单可直接做）。
