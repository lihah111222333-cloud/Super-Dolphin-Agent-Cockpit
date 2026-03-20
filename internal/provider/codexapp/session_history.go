package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func (s *session) ReadHistory(ctx context.Context, threadID string, limit int) ([]dto.Message, error) {
	if s.history == nil {
		return nil, errors.New("codexapp: history backend is not configured")
	}
	target := strings.TrimSpace(firstNonEmpty(threadID, s.threadID))
	if target == "" {
		return nil, errors.New("codexapp: thread id is required")
	}
	messages, err := s.history.ReadHistory(ctx, target, limit)
	if err != nil {
		return nil, err
	}
	return toProviderHistory(messages), nil
}

func toProviderHistory(messages []Message) []dto.Message {
	out := make([]dto.Message, 0, len(messages))
	for _, msg := range messages {
		out = append(out, dto.Message{
			Role:      msg.Role,
			Content:   msg.Content,
			Timestamp: parseCodexHistoryTime(msg.Timestamp),
			Metadata:  decodeHistoryMetadata(msg.Metadata),
		})
	}
	return out
}

func decodeHistoryMetadata(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload) == 0 {
		return nil
	}
	return payload
}

func parseCodexHistoryTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed
	}
	parsed, _ := time.Parse(time.RFC3339, raw)
	return parsed
}
