package thread

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const eventTypeAgentMessage = "agent_message"

func (s *service) ReadHistory(ctx context.Context, threadID string, limit int) ([]dto.Message, error) {
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return nil, err
	}
	targetID := historyTargetID(binding, threadID)
	return session.ReadHistory(ctx, targetID, shared.ClampLimit(limit, 0, 0, 0))
}

type threadReadProvider interface {
	ReadThreadHistory(ctx context.Context, threadID string) (*ReadHistoryResult, error)
}

type runtimeConfigReaderSession interface {
	RuntimeConfigSnapshot() map[string]any
}

func (s *service) ReadMessages(ctx context.Context, threadID string, limit int, before string) (dto.ThreadMessagesResult, error) {
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return dto.ThreadMessagesResult{}, err
	}
	// Ensure session is resumed in background when thread is loaded after restart.
	s.backgroundResumeIfNeeded(ctx, threadID)
	agentID := agentIDFromBinding(binding, threadID)
	all, err := s.readMessagesSource(ctx, threadID, binding)
	if err != nil {
		return dto.ThreadMessagesResult{}, err
	}
	all = decorateThreadMessages(agentID, all)
	page, err := selectMessagesPage(all, limit, before)
	if err != nil {
		return dto.ThreadMessagesResult{}, err
	}
	total := int64(len(all))
	s.publishMessagesPage(threadID, int(total), pageCount(int(total), limit))
	return dto.ThreadMessagesResult{Messages: page, Total: total}, nil
}

func (s *service) ReadRuntimeConfig(ctx context.Context, threadID string) (map[string]any, error) {
	session, binding, err := s.resolveSession(ctx, threadID)
	offline, offlineErr := s.buildOfflineConfig(ctx, threadID, binding)
	if offlineErr != nil {
		return nil, offlineErr
	}
	if err != nil {
		return shared.CloneRuntimeConfigMap(offline.Runtime), nil
	}
	reader, ok := session.(runtimeConfigReaderSession)
	if !ok {
		return shared.CloneRuntimeConfigMap(offline.Runtime), nil
	}
	return mergeRuntimeConfig(offline.Runtime, reader.RuntimeConfigSnapshot()), nil
}

func newThreadReadHandler(svc Service) handler.Func {
	return newThreadCall(func(ctx context.Context, id string) (any, error) {
		if reader, ok := svc.(threadReadProvider); ok {
			return reader.ReadThreadHistory(ctx, id)
		}
		return fallbackReadThreadHistory(ctx, svc, id)
	})
}

func (s *service) ReadThreadHistory(ctx context.Context, threadID string) (*ReadHistoryResult, error) {
	ref, err := s.Get(ctx, threadID)
	if err != nil {
		return nil, err
	}
	fallbackID := readHistoryFallbackID(ref, threadID)
	session, _, err := s.resolveSession(ctx, threadID)
	if err != nil {
		// Session not active (e.g. app restarted). Trigger background resume
		// so the session is ready by the time the user sends a message.
		s.backgroundResumeIfNeeded(ctx, threadID)
		return buildReadHistoryResult(fallbackID), nil
	}
	threads, err := session.ListThreads(ctx)
	if err != nil {
		return nil, err
	}
	return buildReadHistoryResultFromThreads(threads, fallbackID), nil
}

func fallbackReadThreadHistory(ctx context.Context, svc Service, threadID string) (*ReadHistoryResult, error) {
	ref, err := svc.Get(ctx, threadID)
	if err != nil {
		return nil, err
	}
	return buildReadHistoryResult(readHistoryFallbackID(ref, threadID)), nil
}

func buildReadHistoryResultFromThreads(threads []dto.ThreadRef, fallbackID string) *ReadHistoryResult {
	ids := make([]string, 0, len(threads))
	for _, thread := range threads {
		ids = append(ids, thread.ID)
	}
	result := buildReadHistoryResult(ids...)
	if len(result.History) != 0 {
		return result
	}
	return buildReadHistoryResult(fallbackID)
}

func readHistoryFallbackID(ref *Ref, threadID string) string {
	fallbackID := strings.TrimSpace(threadID)
	if ref != nil {
		return shared.FirstNonEmpty(ref.ID, fallbackID)
	}
	return fallbackID
}

func buildReadHistoryResult(threadIDs ...string) *ReadHistoryResult {
	result := ReadHistoryResult{History: make([]ReadHistoryThread, 0, len(threadIDs))}
	seen := make(map[string]struct{}, len(threadIDs))
	for _, threadID := range threadIDs {
		id := strings.TrimSpace(threadID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result.History = append(result.History, ReadHistoryThread{ThreadID: id})
	}
	return &result
}

func mergeRuntimeConfig(base, overlay map[string]any) map[string]any {
	if len(base) == 0 {
		return shared.CloneRuntimeConfigMap(overlay)
	}
	merged := shared.CloneRuntimeConfigMap(base)
	if len(overlay) == 0 {
		return merged
	}
	for key, value := range overlay {
		if nested, ok := value.(map[string]any); ok {
			current, _ := merged[key].(map[string]any)
			merged[key] = mergeRuntimeConfig(current, nested)
			continue
		}
		merged[key] = value
	}
	return merged
}

func decorateThreadMessages(agentID string, messages []dto.Message) []dto.Message {
	out := make([]dto.Message, 0, len(messages))
	fallbackAgentID := strings.TrimSpace(agentID)
	for i, msg := range messages {
		next := msg
		if next.ID == 0 {
			next.ID = int64(i + 1)
		}
		if strings.TrimSpace(next.AgentID) == "" {
			next.AgentID = fallbackAgentID
		}
		if strings.TrimSpace(next.EventType) == "" {
			next.EventType = defaultEventTypeForRole(next.Role)
		}
		next.Method = strings.TrimSpace(next.Method)
		out = append(out, next)
	}
	return out
}

func selectMessagesPage(all []dto.Message, limit int, before string) ([]dto.Message, error) {
	cursor := strings.TrimSpace(before)
	switch {
	case cursor == "":
		return paginateMessagesByID(all, limit, 0), nil
	case strings.IndexFunc(cursor, func(r rune) bool { return r < '0' || r > '9' }) == -1:
		return paginateMessagesByID(all, limit, clampBeforeID(cursor)), nil
	default:
		cutoff, err := parseBeforeCursor(cursor)
		if err != nil {
			return nil, err
		}
		return paginateMessagesBeforeTime(all, limit, cutoff), nil
	}
}

func clampBeforeID(raw string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
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

func paginateMessagesBeforeTime(messages []dto.Message, limit int, cutoff time.Time) []dto.Message {
	filtered := make([]dto.Message, 0, len(messages))
	for _, msg := range messages {
		if !msg.Timestamp.IsZero() && !msg.Timestamp.Before(cutoff) {
			continue
		}
		filtered = append(filtered, msg)
	}
	return paginateMessagesByID(filtered, limit, 0)
}

func paginateMessagesByID(messages []dto.Message, limit int, before int64) []dto.Message {
	if len(messages) == 0 {
		return []dto.Message{}
	}
	page := make([]dto.Message, 0, len(messages))
	for idx := len(messages) - 1; idx >= 0; idx-- {
		if before > 0 && messages[idx].ID >= before {
			continue
		}
		page = append(page, messages[idx])
		if limit > 0 && len(page) >= limit {
			break
		}
	}
	return page
}

func defaultEventTypeForRole(role string) string {
	if strings.EqualFold(strings.TrimSpace(role), "assistant") {
		return eventTypeAgentMessage
	}
	return ""
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
	result := dto.ThreadCompactResult{
		ThreadID:     strings.TrimSpace(threadID),
		Command:      "/compact",
		BeforeTokens: beforeTokens,
		AfterTokens:  afterTokens,
		Compacted:    afterTokens < beforeTokens,
		Estimated:    true,
	}
	s.publishThreadCompacted(result)
	if err := s.invalidatePromptAssembly(ctx, contract.InvalidateCompact); err != nil {
		return dto.ThreadCompactResult{}, err
	}
	return result, nil
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
