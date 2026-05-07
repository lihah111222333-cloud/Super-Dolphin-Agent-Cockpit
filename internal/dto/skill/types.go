// Package skill contains pure-data types (DTOs) for the skill subsystem.
// These types were originally defined in module/skilllibrary but are extracted
// here so that sibling modules (fbsd, uistate, etc.) can depend on a shared
// DTO layer without creating horizontal module→module imports.
package skill

// Origin 表示 skill 来源（spec §5.1）。
type Origin string

const (
	OriginBuiltin     Origin = "builtin"
	OriginMarketplace Origin = "marketplace"
	OriginLocal       Origin = "local"
	OriginDevOverride Origin = "dev-override"
)

// SkillMeta 是 .skill-meta.json sidecar 的完整 schema（spec §3.1）。
type SkillMeta struct {
	Name                   string              `json:"name"`
	Origin                 Origin              `json:"origin"`
	Version                string              `json:"version"`
	VersionHash            string              `json:"version_hash"`
	InstalledAt            string              `json:"installed_at,omitempty"`
	Signature              *string             `json:"signature"`
	AllowedTools           []string            `json:"allowed_tools,omitempty"`
	DisableModelInvocation bool                `json:"disable_model_invocation,omitempty"`
	Pinned                 bool                `json:"pinned,omitempty"`
	Disabled               bool                `json:"disabled,omitempty"`
	ReplacesNative         map[string][]string `json:"replaces_native,omitempty"`
	SectionSummaries       map[string]string   `json:"section_summaries,omitempty"`
}

// SkillEntry 是一条已扫描的 library 条目。
type SkillEntry struct {
	Dir     string     // 该 skill 在 library 的绝对目录
	SkillMD string     // SKILL.md 字节内容（保持 string 易于直接 hash/parse）
	Meta    *SkillMeta // 同目录下的 .skill-meta.json
}
