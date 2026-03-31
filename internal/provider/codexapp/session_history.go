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
	target := strings.TrimSpace(firstNonEmpty(threadID, s.ThreadID()))
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

func (s *session) CompactThread(ctx context.Context, threadID, args string) error {
	target := s.resolveThreadID(threadID)
	if target == "" {
		return errors.New("codexapp: thread id is required")
	}
	params := map[string]any{"threadId": target}
	if arg := strings.TrimSpace(args); arg != "" {
		params["args"] = arg
	}
	callCtx, cancel := withTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := s.callTransport(callCtx, "thread/compact/start", params)
	return err
}
