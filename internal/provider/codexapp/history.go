package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type Message struct {
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	Timestamp string          `json:"timestamp,omitempty"`
}

type rolloutReader struct{ transport *transport }

func (r *rolloutReader) ReadHistory(ctx context.Context, threadID string, limit int) ([]Message, error) {
	if messages, err := readLocalRollout(threadID, limit); err == nil && len(messages) > 0 {
		return messages, nil
	}
	if r.transport == nil {
		return nil, fmt.Errorf("codexapp: no history source for %s", threadID)
	}
	callCtx, cancel := withTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, err := r.transport.Call(callCtx, "thread/read", map[string]any{"threadId": threadID})
	if err != nil {
		return nil, err
	}
	var resp struct {
		History []Message `json:"history"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	return trimMessages(resp.History, limit), nil
}
