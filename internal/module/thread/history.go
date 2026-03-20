package thread

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func (s *service) ReadHistory(ctx context.Context, threadID string, limit int) ([]dto.Message, error) {
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return nil, err
	}
	targetID := historyTargetID(binding, threadID)
	return session.ReadHistory(ctx, targetID, normalizeHistoryLimit(limit))
}

func (s *service) ReadMessages(ctx context.Context, threadID string, limit int, before string) ([]dto.Message, error) {
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return nil, err
	}
	targetID := historyTargetID(binding, threadID)
	cursor := strings.TrimSpace(before)
	historyLimit := normalizeHistoryLimit(limit)
	if cursor != "" {
		historyLimit = 0
	}
	messages, err := session.ReadHistory(ctx, targetID, historyLimit)
	if err != nil {
		return nil, err
	}
	if cursor == "" {
		return messages, nil
	}
	cutoff, err := parseBeforeCursor(cursor)
	if err != nil {
		return nil, err
	}
	return filterMessagesBefore(messages, limit, cutoff), nil
}

func normalizeHistoryLimit(limit int) int {
	if limit < 0 {
		return 0
	}
	return limit
}

func parseBeforeCursor(raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, errors.New("before cursor is required")
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	numeric, err := strconv.ParseInt(value, 10, 64)
	if err != nil || numeric <= 0 {
		return time.Time{}, errors.New("invalid before cursor")
	}
	switch {
	case numeric >= 1_000_000_000_000_000_000:
		return time.Unix(0, numeric), nil
	case numeric >= 1_000_000_000_000_000:
		return time.UnixMicro(numeric), nil
	case numeric >= 1_000_000_000_000:
		return time.UnixMilli(numeric), nil
	default:
		return time.Unix(numeric, 0), nil
	}
}

func filterMessagesBefore(messages []dto.Message, limit int, cutoff time.Time) []dto.Message {
	filtered := make([]dto.Message, 0, len(messages))
	for _, msg := range messages {
		if !msg.Timestamp.IsZero() && !msg.Timestamp.Before(cutoff) {
			continue
		}
		filtered = append(filtered, msg)
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return filtered
}
