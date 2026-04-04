package claudecli

import (
	"context"
	"errors"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

func (s *session) ReadHistory(ctx context.Context, threadID string, limit int) ([]dto.Message, error) {
	if s.history == nil {
		return nil, errors.New("claudecli: history backend is not configured")
	}
	target := strings.TrimSpace(threadID)
	if target == "" {
		target = strings.TrimSpace(s.ThreadID())
	}
	messages, err := s.history.ReadHistory(ctx, target)
	if err != nil {
		return nil, err
	}
	// Fallback: when the requested threadID (e.g. agentID) has no history,
	// try the session's resolved threadID (e.g. claude UUID from system:init).
	if len(messages) == 0 {
		resolved := strings.TrimSpace(s.ThreadID())
		if resolved != "" && resolved != target {
			if fallback, err := s.history.ReadHistory(ctx, resolved); err == nil && len(fallback) > 0 {
				messages = fallback
			}
		}
	}
	messages = trimClaudeHistory(messages, limit)
	return toProviderHistory(messages), nil
}

func trimClaudeHistory(messages []Message, limit int) []Message {
	messages = normalizeClaudeHistory(messages)
	if limit <= 0 || len(messages) <= limit {
		return messages
	}
	return append([]Message(nil), messages[len(messages)-limit:]...)
}

func toProviderHistory(messages []Message) []dto.Message {
	out := make([]dto.Message, 0, len(messages))
	for _, msg := range messages {
		out = append(out, dto.Message{
			Role:      msg.Role,
			Content:   msg.Content,
			Metadata:  platformshared.DecodeHistoryMetadata(msg.Metadata),
			Timestamp: platformshared.ParseRFC3339Loose(msg.Timestamp),
		})
	}
	return out
}
