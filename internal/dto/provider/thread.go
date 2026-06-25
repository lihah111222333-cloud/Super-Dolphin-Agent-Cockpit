package provider

// ThreadRef 是 thread 的轻量引用，用于列表和选择器场景，不含完整配置。
type ThreadRef struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Archived bool   `json:"archived,omitempty"`
}
