package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
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

func (s *session) ReadHistory(ctx context.Context, threadID string, limit int) ([]dto.Message, error) {
	if s.history == nil {
		return nil, errors.New("codexapp: history backend is not configured")
	}
	target, err := requireThreadID(s, threadID)
	if err != nil {
		return nil, err
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
	return decodeJSONMap(raw)
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

func (s *session) CompactThread(ctx context.Context, threadID, args string) error {
	target, err := requireThreadID(s, threadID)
	if err != nil {
		return err
	}
	params := map[string]any{"threadId": target}
	if arg := strings.TrimSpace(args); arg != "" {
		params["args"] = arg
	}
	_, err = callWithTimeout(ctx, callTargetFunc(s.callTransport), 10*time.Second, "thread/compact/start", params)
	return err
}
