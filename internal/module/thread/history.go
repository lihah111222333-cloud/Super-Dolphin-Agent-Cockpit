package thread

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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
		s.publishMessagesPage(threadID, len(messages), pageCount(len(messages), limit))
		return messages, nil
	}
	cutoff, err := parseBeforeCursor(cursor)
	if err != nil {
		return nil, err
	}
	filtered := filterMessagesBefore(messages, limit, cutoff)
	s.publishMessagesPage(threadID, len(filtered), pageCount(len(filtered), limit))
	return filtered, nil
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

func (s *service) Compact(ctx context.Context, threadID, args string) (dto.ThreadCompactResult, error) {
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return dto.ThreadCompactResult{}, err
	}
	provider := bindingProvider(binding)
	compactor, ok := session.(compactSession)
	if !ok {
		return dto.ThreadCompactResult{}, newFriendlyCapabilityError(
			dto.CapContextCompact,
			provider,
			errContextCompactUnsupported,
		)
	}
	targetID := historyTargetID(binding, threadID)
	beforeTokens, err := estimateThreadTokens(ctx, session, targetID)
	if err != nil {
		return dto.ThreadCompactResult{}, err
	}
	if err := compactor.CompactThread(ctx, targetID, args); err != nil {
		return dto.ThreadCompactResult{}, wrapFriendlyCapabilityError(
			err,
			dto.CapContextCompact,
			provider,
			errContextCompactUnsupported,
		)
	}
	afterTokens, err := compactAfterTokens(ctx, session, targetID, beforeTokens)
	if err != nil {
		return dto.ThreadCompactResult{}, err
	}
	return dto.ThreadCompactResult{
		ThreadID:     strings.TrimSpace(threadID),
		Command:      "/compact",
		BeforeTokens: beforeTokens,
		AfterTokens:  afterTokens,
		Compacted:    afterTokens < beforeTokens,
		Estimated:    true,
	}, nil
}

func estimateThreadTokens(ctx context.Context, session contract.Session, threadID string) (int, error) {
	messages, err := session.ReadHistory(ctx, threadID, 0)
	if err != nil {
		return 0, err
	}
	return estimateHistoryTokens(messages), nil
}

func compactAfterTokens(ctx context.Context, session contract.Session, threadID string, before int) (int, error) {
	last := before
	for i := 0; i < 3; i++ {
		if i > 0 {
			if err := waitCompactPoll(ctx); err != nil {
				return 0, err
			}
		}
		next, err := estimateThreadTokens(ctx, session, threadID)
		if err != nil {
			return 0, err
		}
		last = next
		if next < before {
			break
		}
	}
	return last, nil
}

func waitCompactPoll(ctx context.Context) error {
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func estimateHistoryTokens(messages []dto.Message) int {
	total := 0
	for _, msg := range messages {
		total += estimateMessageTokens(msg)
	}
	return total
}

func estimateMessageTokens(msg dto.Message) int {
	return estimateTextTokens(msg.Content) + estimateMetadataTokens(msg.Metadata)
}

func estimateMetadataTokens(metadata map[string]any) int {
	if len(metadata) == 0 {
		return 0
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return 0
	}
	return estimateTextTokens(string(raw))
}

func estimateTextTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	runes := utf8.RuneCountInString(text)
	tokens := (runes + 3) / 4
	if tokens < 1 {
		return 1
	}
	return tokens
}
