package provider

import "time"

// Message is a provider-normalized conversation event returned to thread
// history readers.
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

// ThreadMessagesResult is the paged message-history response used by provider
// thread readers.
type ThreadMessagesResult struct {
	Messages   []Message `json:"messages"`
	Total      int64     `json:"total"`
	HasMore    bool      `json:"hasMore"`
	NextBefore string    `json:"nextBefore"`
}

// MessagePageRequest describes an internal provider-history page request.
type MessagePageRequest struct {
	Limit  int
	Before string
}

// MessagePageResult carries provider-history page data before it is mapped to a
// transport response.
type MessagePageResult struct {
	Messages   []Message
	HasMore    bool
	NextBefore string
}
