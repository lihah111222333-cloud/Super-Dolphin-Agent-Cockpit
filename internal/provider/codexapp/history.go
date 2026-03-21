package codexapp

import (
	"context"
	"encoding/json"
	"log/slog"
)

type Message struct {
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	Timestamp string          `json:"timestamp,omitempty"`
}

type rolloutReader struct {
	logger    *slog.Logger
	transport *transport
}

func (r *rolloutReader) ReadHistory(ctx context.Context, threadID string, limit int) ([]Message, error) {
	if messages, err := readLocalRollout(threadID, limit); err == nil && len(messages) > 0 {
		return messages, nil
	} else if err != nil && r.logger != nil {
		r.logger.Warn("codexapp: local rollout history unavailable", "thread_id", threadID, "error", err)
	}
	if r.logger != nil {
		r.logger.Warn("codexapp: remote history API unavailable; returning empty history", "thread_id", threadID)
	}
	return []Message{}, nil
}
