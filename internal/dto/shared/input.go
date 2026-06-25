package shared

// InputItem 是 turn/steer 请求中的单条用户输入，支持文本、文件路径、URL 等类型。
type InputItem struct {
	Type    string `json:"type"`              // 输入类型，如 "text"、"file"、"url"。
	Content string `json:"content,omitempty"` // 文本内容。
	Path    string `json:"path,omitempty"`    // 文件路径（type="file" 时使用）。
	Name    string `json:"name,omitempty"`    // 显示名称或文件名。
	URL     string `json:"url,omitempty"`     // 资源 URL（type="url" 时使用）。
}
