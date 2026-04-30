package fbsd

import "time"

// 默认双层合并参数（spec §9.7）。
const (
	DefaultWorkspaceMinCalls = 10  // ws ≥ 此值时全用 ws 数据
	DefaultWorkspaceWeight   = 0.3 // ws < 阈值时混合权重：weight*ws + (1-weight)*global
)

// EffectiveScore 把 workspace + global 双层数据合并为最终 score，对应 spec §9.3：
//   - ws 调用数 ≥ minCalls：仅用 ws 数据（局部使用频繁，全局信号视为噪声）
//   - 否则：weight*Score(ws) + (1-weight)*Score(global) 混合（缓启动）
//   - ws == nil：直接用 global
//   - 全 nil 或两边都无 calls：返回 0
//
// 时间通过 now 注入便于测试；halfLife / frozen 同 Score 语义。
func EffectiveScore(ws, glob *SkillStats, now time.Time, halfLife, frozen time.Duration, minCalls int, weight float64) float64 {
	wsTotal := 0
	if ws != nil {
		wsTotal = len(ws.Calls)
	}
	if wsTotal >= minCalls {
		return Score(ws, now, halfLife, frozen)
	}
	if glob == nil {
		if ws == nil {
			return 0
		}
		// 仅 ws 有数据但不足阈值；本期 spec §9.3 未明示该 corner，按"weight*ws"
		// 处理：缓启动场景下避免单边 100% 计入。
		return weight * Score(ws, now, halfLife, frozen)
	}
	if ws == nil {
		return Score(glob, now, halfLife, frozen)
	}
	return weight*Score(ws, now, halfLife, frozen) + (1-weight)*Score(glob, now, halfLife, frozen)
}
