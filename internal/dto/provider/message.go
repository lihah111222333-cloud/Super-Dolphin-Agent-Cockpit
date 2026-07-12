package provider

import "time"

// Message 是 provider 层的消息 DTO，对应一条 thread 历史记录。
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

// ThreadMessagesResult 是 thread 消息分页查询的响应结果，包含游标信息。
type ThreadMessagesResult struct {
	Messages   []Message `json:"messages"`
	Total      int64     `json:"total"`
	HasMore    bool      `json:"hasMore"`
	NextBefore string    `json:"nextBefore"` // 下一页的 before 游标。
}

// MessagePageRequest 是消息分页查询的请求参数。
type MessagePageRequest struct {
	Limit  int
	Before string // 游标，查询该 ID 之前的消息。
}

// MessagePageResult 是消息分页查询的内部结果，不含总数字段。
type MessagePageResult struct {
	Messages       []Message
	HasMore        bool
	NextBefore     string
	SourceRevision string `json:"sourceRevision"`
}
