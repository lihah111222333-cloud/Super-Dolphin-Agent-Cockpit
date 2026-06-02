package provider

import "time"

type Message struct {
	ID        int64          `json:"id"`
	AgentID   string         `json:"agentId"`
	Role      string         `json:"role"`
	EventType string         `json:"eventType"`
	Method    string         `json:"method"`
	Content   string         `json:"content"`
	Timestamp time.Time      `json:"createdAt"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type ThreadMessagesResult struct {
	Messages   []Message `json:"messages"`
	Total      int64     `json:"total"`
	HasMore    bool      `json:"hasMore"`
	NextBefore string    `json:"nextBefore"`
}

type MessagePageRequest struct {
	Limit  int
	Before string
}

type MessagePageResult struct {
	Messages   []Message
	HasMore    bool
	NextBefore string
}
