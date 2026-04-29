// Package fbsd implements spec §9 Frequency-Based Skill Disclosure: per-skill
// call-frequency tracking, exponential-decay scoring, dual-layer (workspace +
// global) merge, and budget-driven tier assignment (Hot / Warm / Cold / Frozen).
//
// All public APIs treat nil receivers as no-op so the FBSD feature flag can
// gate the system off without forcing every caller to nil-check.
package fbsd

import "time"

// Tier 是 spec §9.1 定义的 4 档分级。Frozen 表示"不进 L1 manifest"，模型
// 仍可通过未来的 skill_list_all 工具发现（P6.x）。
type Tier string

const (
	TierHot    Tier = "Hot"
	TierWarm   Tier = "Warm"
	TierCold   Tier = "Cold"
	TierFrozen Tier = "Frozen"
)

// CallEvent 是单次 skill 调用打点。
//   - At     Unix 时间，由 tracker 注入；测试可注入 fake clock。
//   - Anchor 为空表示"整 skill 触发"（无 section 级别信息），仍计入 score。
type CallEvent struct {
	SkillName string
	Anchor    string
	At        time.Time
}

// SkillStats 是单条 skill 的累积统计。
//   - Calls        所有调用时间戳，按时间顺序追加；score 计算从中按 frozen
//     window 切片。
//   - InstalledAt  来自 SkillMeta；用于 grace period 判定。空时 grace 不生效。
//   - SectionCalls anchor → count 的精细计数；为 P6.x section-level
//     decay / per-section tier 留接口。
type SkillStats struct {
	Calls        []time.Time    `json:"calls"`
	InstalledAt  time.Time      `json:"installed_at,omitempty"`
	SectionCalls map[string]int `json:"section_calls,omitempty"`
}

// Stats 是 store 单文件 JSON 模型；map key 是 skill name。
type Stats map[string]*SkillStats
