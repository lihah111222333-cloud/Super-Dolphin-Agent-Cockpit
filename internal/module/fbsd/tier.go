package fbsd

import (
	"sort"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

// TierConfig 是 budget 贪心分配的输入参数。env 覆盖在 Tracker / NewTrackerFromEnv
// 构造时注入；测试可直接构造 TierConfig 不经 env。
type TierConfig struct {
	Budget         int           // 总 char budget；Hot/Warm/Cold 累加耗用
	HotChars       int           // 单 Hot skill 渲染估算
	WarmChars      int           // 单 Warm
	ColdChars      int           // 单 Cold
	GraceDuration  time.Duration // 新装 skill 的 grace 期
	HalfLife       time.Duration
	FrozenDuration time.Duration
	WSMinCalls     int     // ws ≥ 此值用 ws-only
	WSWeight       float64 // ws < 阈值时混合权重
}

// DefaultTierConfig 返回 spec §9.7 默认值。
func DefaultTierConfig() TierConfig {
	return TierConfig{
		Budget:         8192,
		HotChars:       600,
		WarmChars:      200,
		ColdChars:      80,
		GraceDuration:  7 * 24 * time.Hour,
		HalfLife:       time.Duration(DefaultHalfLifeDays) * 24 * time.Hour,
		FrozenDuration: time.Duration(DefaultFrozenDays) * 24 * time.Hour,
		WSMinCalls:     DefaultWorkspaceMinCalls,
		WSWeight:       DefaultWorkspaceWeight,
	}
}

// TierAssignment 是 AssignTiers 单条输出。Score 是辅助诊断字段（pinned / grace
// 用 sentinel 大数填，调用方仅需关心 Tier）。
type TierAssignment struct {
	Skill skilllibrary.SkillEntry
	Tier  Tier
	Score float64
}

// pinned/grace 用 sentinel 数值排序确保优先于真实 score；最高 score 也不可能
// 超过这两个 sentinel（实际跑数年也不到 1e10 量级）。
const (
	sentinelPinned = 1e18
	sentinelGrace  = 1e17
)

// AssignTiers 把 active skill 按 spec §9.4 分到 Hot/Warm/Cold/Frozen。
// 优先级（高→低）：
//  1. Pinned：永远 Hot 优先
//  2. Grace 期（installed_at 距 now < cfg.GraceDuration）：Hot 优先（次于 pinned）
//  3. EffectiveScore：按数值降序
//
// disabled skill 不参与分配。score == 0 且非 pinned/非 grace → 直接 Frozen。
// budget 用尽后剩余 → Frozen（贪心：先 Hot，余量降级 Warm/Cold）。
//
// wsStats / globStats 可为 nil（首启动）；时间通过 now 注入便于测试。
func AssignTiers(entries []skilllibrary.SkillEntry, wsStats, globStats Stats, cfg TierConfig, now time.Time) []TierAssignment {
	dec := decorate(entries, wsStats, globStats, cfg, now)
	sort.SliceStable(dec, func(i, j int) bool { return dec[i].score > dec[j].score })
	return greedyAssign(dec, cfg)
}

type decorated struct {
	entry  skilllibrary.SkillEntry
	score  float64
	forced Tier // "" 或 Hot
}

// decorate 把 SkillEntry 按 pinned/grace/score 三档转成 decorated。
func decorate(entries []skilllibrary.SkillEntry, wsStats, globStats Stats, cfg TierConfig, now time.Time) []decorated {
	out := make([]decorated, 0, len(entries))
	for _, e := range entries {
		if e.Meta == nil || e.Meta.Disabled {
			continue
		}
		switch {
		case e.Meta.Pinned:
			out = append(out, decorated{entry: e, score: sentinelPinned, forced: TierHot})
		case withinGrace(e.Meta.InstalledAt, cfg.GraceDuration, now):
			out = append(out, decorated{entry: e, score: sentinelGrace, forced: TierHot})
		default:
			s := EffectiveScore(wsStats[e.Meta.Name], globStats[e.Meta.Name], now, cfg.HalfLife, cfg.FrozenDuration, cfg.WSMinCalls, cfg.WSWeight)
			out = append(out, decorated{entry: e, score: s})
		}
	}
	return out
}

// greedyAssign 从已排序的 decorated 列表按 budget 顺序分配 Hot/Warm/Cold/Frozen。
func greedyAssign(dec []decorated, cfg TierConfig) []TierAssignment {
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
