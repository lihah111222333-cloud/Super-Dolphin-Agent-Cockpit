package claudecli

import "encoding/json"

// Message 表示从 Claude CLI 历史 JSONL 归一化出的单条消息。
// 该结构是 provider history 与统一 DTO 转换之间的边界，字段需保持历史文件 wire 兼容。
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
