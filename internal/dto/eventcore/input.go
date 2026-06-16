package eventcore

// InputItem is a normalized input payload item accepted by thread and tool flows.
type InputItem struct {
	Type    string `json:"type"`
	Content string `json:"content,omitempty"`
	Path    string `json:"path,omitempty"`
	Name    string `json:"name,omitempty"`
	URL     string `json:"url,omitempty"`
}
