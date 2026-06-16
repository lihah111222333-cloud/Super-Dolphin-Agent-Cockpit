package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type Message struct {
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	Timestamp string          `json:"timestamp,omitempty"`
}

type rolloutReader struct {
	logger    *pkglogger.Logger
	transport *transport
}

// ReadHistory 读取history。
func (r *rolloutReader) ReadHistory(ctx context.Context, threadID, codexHome string, limit int) ([]Message, error) {
	if messages, err := readLocalRollout(threadID, codexHome, limit); err == nil && len(messages) > 0 {
		return messages, nil
	} else if err != nil && r.logger != nil {
		r.logger.Warn("codexapp: local rollout history unavailable", "thread_id", threadID, "error", err)
	}
	if r.logger != nil {
		r.logger.Warn("codexapp: remote history API unavailable; returning empty history", "thread_id", threadID)
	}
	return []Message{}, nil
}

// ReadHistory 读取history。
func (s *session) ReadHistory(ctx context.Context, threadID string, limit int) ([]dto.Message, error) {
	if s.history == nil {
		return nil, errors.New("codexapp: history backend is not configured")
	}
	target, err := requireThreadID(s, threadID)
	if err != nil {
		return nil, err
	}
	codexHome := s.runtimeConfigString("codexHome")
	messages, primaryErr := s.history.ReadHistory(ctx, target, codexHome, limit)
	if primaryErr == nil && len(messages) > 0 {
		return toProviderHistory(messages), nil
	}
	if fallback := s.readHistoryFallback(ctx, target, codexHome, limit); fallback != nil {
		return toProviderHistory(fallback), nil
	}
	if primaryErr != nil {
		return nil, primaryErr
	}
	return toProviderHistory(messages), nil
}

// readHistoryFallback tries the session's actual codex thread UUID when the
// primary threadID failed to find rollout history. This handles the case where
// the binding's provider_thread_id wasn't updated (e.g. due to a prior binding
// conflict) but the session knows the correct UUID from the live codex process.
// readHistoryFallback 读取history兜底。
func (s *session) readHistoryFallback(ctx context.Context, primaryTarget, codexHome string, limit int) []Message {
	codexThreadID := strings.TrimSpace(s.ThreadID())
	if codexThreadID == "" || codexThreadID == primaryTarget {
		return nil
	}
	messages, err := s.history.ReadHistory(ctx, codexThreadID, codexHome, limit)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("codexapp: history fallback read failed",
				"requested", primaryTarget,
				"used", codexThreadID,
				"error", err)
		}
		return []Message{}
	}
	if len(messages) == 0 {
		return nil
	}
	if s.logger != nil {
		s.logger.Info("codexapp: history fallback to codex thread UUID",
			"requested", primaryTarget, "used", codexThreadID)
	}
	return messages
}

func toProviderHistory(messages []Message) []dto.Message {
	out := make([]dto.Message, 0, len(messages))
	for _, msg := range messages {
		out = append(out, dto.Message{
			Role:      msg.Role,
			Content:   msg.Content,
			Timestamp: platformshared.ParseRFC3339Loose(msg.Timestamp),
			Metadata:  platformshared.DecodeHistoryMetadata(msg.Metadata),
		})
	}
	return out
}

// CompactThread 处理紧凑列表线程。
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
