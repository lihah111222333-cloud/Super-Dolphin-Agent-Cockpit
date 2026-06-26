package claudecli

import "encoding/json"

type Message struct {
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	Timestamp string          `json:"timestamp,omitempty"`
}

type historyLine struct {
	Type      string         `json:"type"`
	Timestamp string         `json:"timestamp"`
	Message   historyMessage `json:"message"`
}

type historyMessage struct {
	Role    string               `json:"role"`
	Content []historyContentItem `json:"content"`
}

type historyContentItem struct {
	Type   string              `json:"type"`
	Text   string              `json:"text,omitempty"`
	Source *historyImageSource `json:"source,omitempty"`
}

// historyImageSource 对应 Claude CLI 历史里保存的 Anthropic image source 对象。
// 该结构跨越本地 JSONL 和前端附件预览，字段需要保持 wire 兼容。
type historyImageSource struct {
	Type      string `json:"type,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

const injectedFileHintsHeader = "The user has attached the following files. Use the Read tool to view them:"
