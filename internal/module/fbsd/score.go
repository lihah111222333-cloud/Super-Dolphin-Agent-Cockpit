package fbsd

import (
	"math"
	"time"
)

// 默认参数（spec §9.7）；env 覆盖在 Tracker / TierConfig 构造时注入。
const (
	DefaultHalfLifeDays = 7
	DefaultFrozenDays   = 90
)

// Score 计算单条 skill 的指数衰减分数，对应 spec §9.2：
//
//	score = sum(2 ^ (-Δt / half_life))   for each call within frozen window
//
// 参数语义：
//   - now      参考时间（注入便于测试）
//   - halfLife 半衰期；call 发生时间距 now 一个 half_life 时贡献 0.5 分
//   - frozen   截断窗口；早于 now-frozen 的 call 视为冻结，贡献 0
//
// nil stats / 空 calls 返回 0；halfLife <= 0 也返回 0（防御性，不 panic）。
func Score(stats *SkillStats, now time.Time, halfLife time.Duration, frozen time.Duration) float64 {
	if stats == nil || len(stats.Calls) == 0 {
		return 0
	}
	hlSec := halfLife.Seconds()
	if hlSec <= 0 {
		return 0
	}
	cutoff := now.Add(-frozen)
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
