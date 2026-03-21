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

func (s *session) ReadConfig(ctx context.Context, threadID string) (dto.ThreadConfig, error) {
	target := s.resolveThreadID(threadID)
	if target == "" {
		return dto.ThreadConfig{}, errors.New("codexapp: thread id is required")
	}
	callCtx, cancel := withTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := s.callTransport(callCtx, "thread/config/get", map[string]any{"threadId": target})
	if err != nil {
		return dto.ThreadConfig{}, err
	}
	return decodeThreadConfig(raw)
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

func decodeThreadConfig(raw []byte) (dto.ThreadConfig, error) {
	var cfg dto.ThreadConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return dto.ThreadConfig{}, err
	}
	if strings.TrimSpace(cfg.ThreadID) == "" {
		return dto.ThreadConfig{}, errors.New("codexapp: invalid thread/config/get response")
	}
	return cfg, nil
}
