package personalization

const (
	profilePreferenceKey      = "personalization.profile"
	maxShortProfileFieldRunes = 80
	maxLongProfileFieldRunes  = 1200
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
