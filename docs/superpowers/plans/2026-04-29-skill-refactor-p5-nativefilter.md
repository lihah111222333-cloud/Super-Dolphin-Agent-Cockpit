# Skill Refactor — Phase 5: Native CLI Filter Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 to implement this plan task-by-task.

> **本计划基于 2026-04-30 完成的 F1 实测**（见 [appendix](../specs/2026-04-29-skill-refactor-design-appendix-cli-test.md)）。spec §8.3 假设的 `Skill(name)` 圆括号语法**实测不生效**；实际正确语法是 `Skill:name` 冒号。本计划已按实测结果调整。

## Goal

落地 spec §8 native CLI 过滤层（F1）：在 spawn Claude 子进程前，把全局基线 + 每条 active skill 的 `replaces_native` 字段聚合，渲染到 `<workspace>/.claude/settings.json` 的 `permissions.deny`，让 Claude 不再把已被本项目同名 skill "替代"的 Anthropic 自带 skill 暴露给模型。

## Architecture

```
~/.super-dolphin/native-cli-filter.json         # 全局基线（用户级）
                +
SkillMeta.ReplacesNative["claude"]            # 每条 active skill 的声明叠加
                ↓
nativefilter.BuildClaudeSettings(...)         # 聚合 + dedup + Skill: 冒号渲染
                ↓
<workspace>/.claude/settings.json             # 每次 spawn Claude CLI 前写入
                ↓
Claude CLI 启动时读取 settings.json，运行时拦截被 deny 的 Skill / Tool
```

**关键决策（基于实测）**：
1. **deny 语法用 `Skill:<name>` 冒号**，不是 spec §8.3 假设的 `Skill(<name>)` 圆括号
2. **deny 是运行时拦截**而非"完全隐藏"——模型仍能在 `/skills` list 看到被 deny 的 skill 名，但实际调用被拒；这对 P5 目标"不让模型调用我们已替代的 native skill"足够
3. **Codex 侧本期不主推**：实测未在 codex 0.121.0 找到等价工具屏蔽机制；schema 留 `codex.disabled_tools` 字段但 enforcement 走 stub，留 TODO
4. **Feature flag** `SUPER_DOLPHIN_NATIVE_FILTER`：默认关（不写 settings.json，保持现有 P2 行为）；显式打开后才生效

## Tech Stack

Go 1.22+；已有 fx 图；已有 SkillMeta.ReplacesNative 字段（P1 schema）；无新增运行时依赖。

## 前置阅读

- spec §8 全文（native filter 设计）
- spec §8.3 + [appendix](../specs/2026-04-29-skill-refactor-design-appendix-cli-test.md)（实测结果决定语法）
- P2 commit `5e3ee29` + `74336ab`（workspace symlink 注入；P5 在同一注入点加 settings.json 写入）

---

## File Structure

**新增**：
```
internal/module/nativefilter/
├── config.go         (Task 1: NativeFilterConfig types + Load)
├── config_test.go
├── aggregate.go      (Task 2: 聚合 + dedup)
├── aggregate_test.go
├── claude.go         (Task 3: BuildClaudeSettings + Skill: 冒号渲染)
├── claude_test.go
├── writer.go         (Task 4: WriteWorkspaceSettings 原子写入)
├── writer_test.go
└── module.go         (Task 5: fx Module + flag 解析)
```

**修改**：
```
internal/provider/claudecli/driver.go         (Task 6: 在 prepareSessionStart 调 nativefilter，feature flag gated)
internal/app/modules.go                       (Task 5: 把 nativefilter.Module 加入 fx 图)
```

无文件删除。

---

## Task 1: nativefilter 包基础 types + JSON 解析

**Files:**
- Create: `internal/module/nativefilter/config.go`
- Test: `internal/module/nativefilter/config_test.go`

### Step 1: Failing test

```go
package nativefilter

import (
    "os"
    "path/filepath"
    "testing"
)

func TestLoadConfig_FullClaude(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "native-cli-filter.json")
    os.WriteFile(path, []byte(`{
      "claude": {
        "disabled_skills": ["simplify", "init"],
        "disabled_tools": ["Read"],
        "allowed_tools": null
      },
      "codex": {
        "disabled_tools": []
      }
    }`), 0o644)
    cfg, err := LoadConfig(path)
    if err != nil { t.Fatal(err) }
    if len(cfg.Claude.DisabledSkills) != 2 || cfg.Claude.DisabledSkills[0] != "simplify" {
        t.Errorf("disabled_skills wrong: %v", cfg.Claude.DisabledSkills)
    }
    if len(cfg.Claude.DisabledTools) != 1 || cfg.Claude.DisabledTools[0] != "Read" {
        t.Errorf("disabled_tools wrong: %v", cfg.Claude.DisabledTools)
    }
}

func TestLoadConfig_MissingFileReturnsEmpty(t *testing.T) {
    cfg, err := LoadConfig("/nonexistent/native-cli-filter.json")
    if err != nil { t.Fatalf("missing file should not error: %v", err) }
    if cfg.Claude.DisabledSkills != nil || cfg.Codex.DisabledTools != nil {
        t.Errorf("missing file should yield empty config, got %+v", cfg)
    }
}

func TestLoadConfig_MalformedJSONReturnsError(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "bad.json")
    os.WriteFile(path, []byte(`{not json`), 0o644)
    if _, err := LoadConfig(path); err == nil {
        t.Fatal("malformed json should return error")
    }
}
```

### Step 2: Minimal implementation

```go
// Package nativefilter renders Anthropic-CLI / OpenAI-codex native skill +
// tool filtering decisions into per-CLI configuration files. See spec §8.
package nativefilter

import (
    "encoding/json"
    "errors"
    "fmt"
    "io/fs"
    "os"
)

// Config 对应 ~/.super-dolphin/native-cli-filter.json schema。
// 缺省字段（如 allowed_tools=null）解析为 nil slice。
type Config struct {
    Claude ClaudeConfig `json:"claude"`
    Codex  CodexConfig  `json:"codex"`
}

type ClaudeConfig struct {
    DisabledSkills []string `json:"disabled_skills,omitempty"`
    DisabledTools  []string `json:"disabled_tools,omitempty"`
    AllowedTools   []string `json:"allowed_tools,omitempty"` // nil = no allowlist
}

type CodexConfig struct {
    DisabledTools []string `json:"disabled_tools,omitempty"`
}

// LoadConfig 读取 path 上的 JSON。文件不存在视为"未配置"返回空 Config。
// 解析失败（malformed JSON / 类型不匹配）返回 error。
func LoadConfig(path string) (Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        if errors.Is(err, fs.ErrNotExist) {
            return Config{}, nil
        }
        return Config{}, fmt.Errorf("nativefilter: read %s: %w", path, err)
    }
    var c Config
    if err := json.Unmarshal(data, &c); err != nil {
        return Config{}, fmt.Errorf("nativefilter: parse %s: %w", path, err)
    }
    return c, nil
}
```

### Step 3-4: test + commit

```
git commit -m "feat(nativefilter): add Config types + LoadConfig (P5 Task 1)"
```

---

## Task 2: replaces_native 聚合 + dedup

**Files:**
- Create: `internal/module/nativefilter/aggregate.go`
- Test: `internal/module/nativefilter/aggregate_test.go`

每条 active skill 的 `SkillMeta.ReplacesNative` 是 `map[string][]string`，key 是 cli 名（"claude" / "codex"），value 是要屏蔽的 native skill / tool 名。Task 2 把所有 enabled skill 的声明聚合 + dedup。

### Step 1: Failing test

```go
func TestAggregate_DeduplicatesAcrossSkills(t *testing.T) {
    entries := []skilllibrary.SkillEntry{
        {Meta: &skilllibrary.SkillMeta{Name: "a", ReplacesNative: map[string][]string{"claude": {"simplify", "init"}}}},
        {Meta: &skilllibrary.SkillMeta{Name: "b", ReplacesNative: map[string][]string{"claude": {"init", "review"}}}},
        {Meta: &skilllibrary.SkillMeta{Name: "c", Disabled: true, ReplacesNative: map[string][]string{"claude": {"loop"}}}},
    }
    got := AggregateReplacesNative(entries, "claude")
    // disabled skill 不算；其它两个聚合 + dedup
    want := []string{"init", "review", "simplify"} // 排序确定输出顺序
    if !reflect.DeepEqual(got, want) {
        t.Errorf("got %v, want %v", got, want)
    }
}
```

### Step 2: Implementation

```go
package nativefilter

import (
    "sort"

    "github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

// AggregateReplacesNative 把所有 enabled skill 的 ReplacesNative[cli] 字段
// 聚合 + dedup + sort，返回稳定顺序。Disabled skill 跳过（与 cache reconcile
// 的"disabled 不进 cache"一致）。
func AggregateReplacesNative(entries []skilllibrary.SkillEntry, cli string) []string {
    seen := make(map[string]struct{})
    for _, e := range entries {
        if e.Meta == nil || e.Meta.Disabled {
            continue
        }
        for _, name := range e.Meta.ReplacesNative[cli] {
            if name == "" {
                continue
            }
            seen[name] = struct{}{}
        }
    }
    out := make([]string, 0, len(seen))
    for n := range seen {
        out = append(out, n)
    }
    sort.Strings(out)
    return out
}
```

### Step 3-4: test + commit

```
git commit -m "feat(nativefilter): aggregate ReplacesNative across active skills (P5 Task 2)"
```

---

## Task 3: BuildClaudeSettings — 渲染 settings.json

**Files:**
- Create: `internal/module/nativefilter/claude.go`
- Test: `internal/module/nativefilter/claude_test.go`

把 base.Claude + extra disabled skills 聚合到 `permissions.deny`，**用 `Skill:<name>` 冒号语法**（实测确认）。

### Step 1: Failing test

```go
func TestBuildClaudeSettings_MergesDenyList(t *testing.T) {
    base := Config{
        Claude: ClaudeConfig{
            DisabledSkills: []string{"native-extra"},
            DisabledTools:  []string{"Read"},
        },
    }
    extra := []string{"simplify", "init"}
    out, err := BuildClaudeSettings(base, extra)
    if err != nil { t.Fatal(err) }

    var got map[string]any
    json.Unmarshal(out, &got)
    perms := got["permissions"].(map[string]any)
    deny := perms["deny"].([]any)
    // 期望：Read（tool） + Skill:simplify + Skill:init + Skill:native-extra（base 也 wrap 进 Skill:）
    wantContains := []string{"Read", "Skill:simplify", "Skill:init", "Skill:native-extra"}
    for _, w := range wantContains {
        found := false
        for _, d := range deny { if d.(string) == w { found = true; break } }
        if !found { t.Errorf("deny missing %q: %v", w, deny) }
    }
}

func TestBuildClaudeSettings_EmptyAllNoDeny(t *testing.T) {
    out, err := BuildClaudeSettings(Config{}, nil)
    if err != nil { t.Fatal(err) }
    // 空配置应返回最小 settings（也可以是 nil 表示"不写 settings"）
    if !bytes.Contains(out, []byte(`"deny":[]`)) && !bytes.Contains(out, []byte(`"deny":null`)) {
        // 任一形式 OK；只要 deny 是空就行
        t.Logf("empty deny: %s", out)
    }
}
```

### Step 2: Implementation

```go
package nativefilter

import (
    "encoding/json"
    "fmt"
)

// claudeSettings 是 BuildClaudeSettings 的输出 JSON 结构。
// 仅含 P5 用到的字段（permissions.deny）；未来扩 enabledPlugins 等再加。
type claudeSettings struct {
    Permissions claudePermissions `json:"permissions"`
}

type claudePermissions struct {
    Deny []string `json:"deny"`
}

// BuildClaudeSettings 把 base.Claude 的 disabled_tools + disabled_skills +
// extra（来自 SkillMeta.ReplacesNative 聚合）渲染成 Claude Code settings.json。
// disabled_skills 和 extra 都用 "Skill:<name>" 冒号格式 wrap。
// disabled_tools 直接进 deny（保留 base 字面）。
func BuildClaudeSettings(base Config, extra []string) ([]byte, error) {
    deny := make([]string, 0, len(base.Claude.DisabledTools)+len(base.Claude.DisabledSkills)+len(extra))
    deny = append(deny, base.Claude.DisabledTools...)
    for _, s := range base.Claude.DisabledSkills {
        deny = append(deny, "Skill:"+s)
    }
    for _, s := range extra {
        deny = append(deny, "Skill:"+s)
    }
    deny = dedupStrings(deny)

    settings := claudeSettings{
        Permissions: claudePermissions{Deny: deny},
    }
    out, err := json.MarshalIndent(settings, "", "  ")
    if err != nil {
        return nil, fmt.Errorf("nativefilter: marshal claude settings: %w", err)
    }
    return out, nil
}

func dedupStrings(in []string) []string {
    seen := make(map[string]struct{}, len(in))
    out := make([]string, 0, len(in))
    for _, s := range in {
        if s == "" { continue }
        if _, ok := seen[s]; ok { continue }
        seen[s] = struct{}{}
        out = append(out, s)
    }
    return out
}
```

### Step 3-4: test + commit

```
git commit -m "feat(nativefilter): render Claude settings.json with Skill: deny syntax (P5 Task 3)"
```

---

## Task 4: WriteWorkspaceSettings — 原子写入

**Files:**
- Create: `internal/module/nativefilter/writer.go`
- Test: `internal/module/nativefilter/writer_test.go`

把 settings JSON 写到 `<workspace>/.claude/settings.json`。复用 P1 的 `tmp + rename` 模式（spec §4.4 同样合规）。

### Step 1: Failing test

```go
func TestWriteWorkspaceSettings_CreatesFile(t *testing.T) {
    ws := t.TempDir()
    body := []byte(`{"permissions":{"deny":["Read"]}}`)
    if err := WriteWorkspaceSettings(ws, body); err != nil { t.Fatal(err) }
    got, err := os.ReadFile(filepath.Join(ws, ".claude", "settings.json"))
    if err != nil { t.Fatal(err) }
    if !bytes.Equal(got, body) { t.Errorf("got %s want %s", got, body) }
}

func TestWriteWorkspaceSettings_OverwritesExisting(t *testing.T) {
    ws := t.TempDir()
    os.MkdirAll(filepath.Join(ws, ".claude"), 0o755)
    os.WriteFile(filepath.Join(ws, ".claude", "settings.json"), []byte("old"), 0o644)
    if err := WriteWorkspaceSettings(ws, []byte("new")); err != nil { t.Fatal(err) }
    got, _ := os.ReadFile(filepath.Join(ws, ".claude", "settings.json"))
    if string(got) != "new" { t.Errorf("got %s", got) }
}
```

### Step 2: Implementation

```go
package nativefilter

import (
    "fmt"
    "os"
    "path/filepath"
)

// WriteWorkspaceSettings 把 body 原子写到 workspaceDir/.claude/settings.json。
// 用 .tmp + rename 避免 Claude CLI 在我们半写状态下读到。
func WriteWorkspaceSettings(workspaceDir string, body []byte) error {
    dir := filepath.Join(workspaceDir, ".claude")
    if err := os.MkdirAll(dir, 0o755); err != nil {
        return fmt.Errorf("nativefilter: mkdir %s: %w", dir, err)
    }
    target := filepath.Join(dir, "settings.json")
    tmp := target + ".tmp"
    if err := os.WriteFile(tmp, body, 0o644); err != nil {
        return fmt.Errorf("nativefilter: write tmp: %w", err)
    }
    if err := os.Rename(tmp, target); err != nil {
        _ = os.Remove(tmp)
        return fmt.Errorf("nativefilter: rename to settings.json: %w", err)
    }
    return nil
}
```

> **简化决策**：此处用单 tmp+rename，**不**复用 P1 atomic.go 的 tmp+backup 双 rename 模式。理由：settings.json 是单文件不是目录，rename 跨平台对单文件覆盖有原子保证；spec §4.4 的 backup 模式针对的是目录树。Claude CLI 重启读到旧 settings 也无害。

### Step 3-4: test + commit

```
git commit -m "feat(nativefilter): atomic workspace settings.json writer (P5 Task 4)"
```

---

## Task 5: fx Module wire-up + Filter 服务

**Files:**
- Create: `internal/module/nativefilter/module.go`
- Modify: `internal/app/modules.go`

封装 base config load + skilllibrary entries 拉取 + render + write 成一个 `Filter` 服务。

### 实现

```go
// internal/module/nativefilter/module.go
package nativefilter

import (
    "fmt"
    "os"
    "path/filepath"

    "github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
    "go.uber.org/fx"
)

var Module = fx.Module("nativefilter",
    fx.Provide(NewFilter),
)

// Filter 是 P5 nativefilter 的对外服务。
//
// Apply(workspaceDir) 在每次 spawn Claude CLI 之前调用：读全局基线 +
// 当前 skilllibrary entries 的 ReplacesNative 聚合 + 渲染并写入
// <workspaceDir>/.claude/settings.json。Feature flag SUPER_DOLPHIN_NATIVE_FILTER
// 关闭时（默认）直接 no-op。
type Filter struct {
    store    *skilllibrary.Store
    baseFn   func() (Config, error)
    enabled  bool
}

func NewFilter(store *skilllibrary.Store) *Filter {
    enabled := os.Getenv("SUPER_DOLPHIN_NATIVE_FILTER") == "on"
    return &Filter{
        store: store,
        baseFn: func() (Config, error) {
            home, err := os.UserHomeDir()
            if err != nil { return Config{}, err }
            return LoadConfig(filepath.Join(home, ".super-dolphin", "native-cli-filter.json"))
        },
        enabled: enabled,
    }
}

func (f *Filter) Apply(workspaceDir string) error {
    if !f.enabled || f == nil || f.store == nil {
        return nil
    }
    base, err := f.baseFn()
    if err != nil {
        return fmt.Errorf("nativefilter: load base config: %w", err)
    }
    entries, err := f.store.List()
    if err != nil {
        return fmt.Errorf("nativefilter: list skill entries: %w", err)
    }
    extra := AggregateReplacesNative(entries, "claude")
    body, err := BuildClaudeSettings(base, extra)
    if err != nil {
        return err
    }
    return WriteWorkspaceSettings(workspaceDir, body)
}
```

### Step: fx wire-up + commit

把 `nativefilter.Module` 加到 `internal/app/modules.go` 的 module list。

```
git commit -m "feat(nativefilter): fx Module + Filter service with feature flag (P5 Task 5)"
```

---

## Task 6: claudecli driver 集成

**File:** `internal/provider/claudecli/driver.go`

P2 已经在 `prepareSessionStart`（spawn Claude CLI 之前）调用 `cliadapter.SetupWorkspaceSkills`。P5 在同一注入点加一行：

```go
// 在 SetupWorkspaceSkills 之后
if d.nativeFilter != nil {
    if err := d.nativeFilter.Apply(workspaceDir); err != nil {
        // 不阻断 spawn——log warn 即可，settings.json 写失败 Claude 仍能跑
        d.logger.Warn("nativefilter apply failed", "err", err)
    }
}
```

driver struct 加 `nativeFilter *nativefilter.Filter` optional 字段（fx inject）。Feature flag 关闭时 `Apply` 自身 no-op，driver 端不必再判 flag。

### 测试

- driver 单元测试：mock `nativefilter.Filter`，验证 `Apply` 被调一次
- 集成测试：feature flag on，跑 spawn 前后看 `.claude/settings.json` 是否真生成 + 内容正确

### Step: commit

```
git commit -m "feat(claudecli): wire nativefilter Apply before spawn (P5 Task 6)"
```

---

## Task 7: 端到端 fixture 测试

**File:** `internal/module/nativefilter/e2e_test.go`（或加入现有集成测试包）

构造一个完整的 fixture：
- 临时 library + cache
- 一个 skill `tdd-replacement` with `ReplacesNative: {"claude": ["simplify"]}`
- 一个 disabled skill `dead-skill` with `ReplacesNative: {"claude": ["loop"]}`
- 一个 base config 写 `disabled_skills: ["init"]`
- Apply 后断言 workspace settings.json 含 `Skill:simplify` + `Skill:init`，**不含** `Skill:loop`（disabled skill 跳过）

---

## Task 8: 全测试 + 冒烟

```
cd /private/tmp/super-dolphin-skill-refactor-p5
go test -short ./internal/...
go build ./...
go vet ./...
```

冒烟：开 `SUPER_DOLPHIN_NATIVE_FILTER=on` 实际启动一次 Claude CLI 在测试 workspace，验证 settings.json 真被读到（复用本 PR 配套 appendix 的 `simplify` 实测命令一行即可）。

---

## 已知偏差与未覆盖项

### 不在 P5 范畴（P5.x / P6 / 后续）

1. **Codex 侧 native tool 屏蔽**：实测 codex 0.121.0 未发现等价机制；schema 字段 `Config.Codex.DisabledTools` 留好但 enforcement 走 stub。`Filter.Apply` 不写任何 codex 配置文件。**真有需求时按 [appendix](../specs/2026-04-29-skill-refactor-design-appendix-cli-test.md) §5 列的方向（翻 codex-cli 0.x 源码 / 物理隔离 ~/.codex/）实测后再补**。
2. **Skill 完全隐藏**：当前是运行时拦截（list 中仍出现，调用被拒）。如未来需要"模型完全看不到名字"，需上 fallback 档位 3（物理隔离 `CLAUDE_CONFIG_DIR`）。
3. **§10 `allowed_tools` enforcement**（P4 plan v2 列入未覆盖）：sidecar 字段仍只 parse 不 enforce。P5 nativefilter 处理的是 native skill / 工具屏蔽，与 per-skill `AllowedTools` 是两个不同维度，**P5 不交叉解决**。

### 已知 spec deviation

- spec §8.3 假设 `Skill(name)` 圆括号语法实测不生效，本计划改用 `Skill:name` 冒号；spec 文本将在 P5 完成后单独 errata 修正。
- spec §8.2 给的 settings.json 示例仍是圆括号格式，同样需要 errata。
- 同名 skill 优先级（appendix T4）未实测；本期假设 workspace skill 不与 user-level plugin 同名（P2 commit 5e3ee29 / 74336ab 注入的是我们自己的 cache 目录，origin 受控）。如未来 marketplace 上线导致同名冲突，需补做 T4。

### 监测

- P5 ship 后建议加一个 ops 指标：每次 `Filter.Apply` 写 settings.json 的耗时 + 失败率，验证 `Apply` 不引入显著启动延迟。
- 灰度策略：`SUPER_DOLPHIN_NATIVE_FILTER=off` 默认；P5 PR 合并后灰度环境先打开 7 天观察，再考虑全量翻 default on。

---

## Phase 5 自审

按 编写计划 §自审：

**1. 规格覆盖**：
- spec §8.1 schema：Task 1 覆盖（Config types）
- spec §8.2 子进程启动前组装：Task 2-6 覆盖（聚合 + 渲染 + 写入 + driver 集成）
- spec §8.3 实测：appendix 已记入；本计划基于实测结果调整（`Skill:name` 冒号 vs `Skill(name)` 圆括号）
- spec §12 P5 行 feature flag：Task 5 实现 `SUPER_DOLPHIN_NATIVE_FILTER` env

未覆盖项明确列入"已知偏差"。

**2. 占位符扫描**：所有代码段为完整可应用编辑。

**3. 类型一致性**：
- `nativefilter.Config` 与 `~/.super-dolphin/native-cli-filter.json` schema 字面对应
- `BuildClaudeSettings` 输出 JSON 与实测验证过的 `permissions.deny: ["Skill:..."]` 字面一致
- `Filter.Apply` 与 driver 的调用约定（在 SetupWorkspaceSkills 之后调）一致

**4. 实测对齐**：
- 主路径 Skill: 冒号语法 ← appendix T1h
- settings.json 路径 ← appendix T2 验证
- feature flag 默认关 ← spec §12 + §13 #10 灰度约束

修复内联：暂无问题。

---

## 执行交接

计划已保存到 `docs/superpowers/plans/2026-04-29-skill-refactor-p5-nativefilter.md`。Tasks 拆分清晰、各 Task 独立 commit，建议子代理驱动模式（每 Task implementer + spec reviewer + code reviewer）。Task 7 端到端测试可以放到最后跟 Task 8 一起跑。
