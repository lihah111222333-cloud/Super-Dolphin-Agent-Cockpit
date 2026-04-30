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

// historyImageSource mirrors the Anthropic Messages API image source object
// that claude CLI persists to its on-disk session history when a turn carried
// a vision content block.
type historyImageSource struct {
	Type      string `json:"type,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

const injectedFileHintsHeader = "The user has attached the following files. Use the Read tool to view them:"
