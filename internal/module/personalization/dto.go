// Package personalization 管理项目级用户个人资料（显示名、职业、背景、自定义说明），
// 并将非空资料注入 prompt 动态 section。
package personalization

const (
	// profilePreferenceKey 是存储个人资料的 uipreference key。
	profilePreferenceKey      = "personalization.profile"
	maxShortProfileFieldRunes = 80   // 短文本字段（displayName/role）的最大字符数。
	maxLongProfileFieldRunes  = 1200 // 长文本字段（background/customInstructions）的最大字符数。
)

// Profile 保存定制角色使用的项目级个人资料，会同时用于 RPC 入参和 prompt 注入。
type Profile struct {
	DisplayName        string `json:"displayName"`
	Role               string `json:"role"`
	Background         string `json:"background"`
	CustomInstructions string `json:"customInstructions"`
}

// ProfileResult 是 profile 读取和保存接口的统一返回结构。
type ProfileResult struct {
	Profile Profile `json:"profile"`
}
