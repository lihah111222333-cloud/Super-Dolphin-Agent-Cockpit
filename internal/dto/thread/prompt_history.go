package thread

import "time"

// PromptHistoryEntry 是跨 thread 聚合后的一条用户 prompt 历史。
type PromptHistoryEntry struct {
	ThreadID  string    `json:"threadId"`
	MessageID string    `json:"messageId"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}

// PromptHistoryResult 是 prompt history 的稳定分页响应。
type PromptHistoryResult struct {
	Entries    []PromptHistoryEntry `json:"entries"`
	NextCursor string               `json:"nextCursor"`
	HasMore    bool                 `json:"hasMore"`
	Nonce      string               `json:"nonce"`
}
