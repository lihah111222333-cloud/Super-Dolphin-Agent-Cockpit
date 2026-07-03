package shared

import "strings"

// InputItem 是 turn/steer 请求中的单条用户输入，支持文本、文件路径、URL 等类型。
type InputItem struct {
	Type    string `json:"type"`              // 输入类型，如 "text"、"mention"、"image"。
	Content string `json:"content,omitempty"` // 文本内容。
	Path    string `json:"path,omitempty"`    // 文件路径（type="mention" 或 "local_image" 时使用）。
	Name    string `json:"name,omitempty"`    // 显示名称或文件名。
	URL     string `json:"url,omitempty"`     // 资源 URL（type="image" 时使用）。
}

// NormalizeInputType 将 turn 输入类型和兼容别名归一化。
// 返回 ok=false 表示调用方传入了不支持的类型，RPC/provider 边界必须 fail-fast。
func NormalizeInputType(value string) (normalized string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "text":
		return "text", true
	case "image":
		return "image", true
	case "localimage", "local_image":
		return "local_image", true
	case "file", "mention":
		return "mention", true
	case "filecontent":
		return "filecontent", true
	default:
		return strings.ToLower(strings.TrimSpace(value)), false
	}
}
