package thread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
)

const eventTypeAgentMessage = "agent_message"
const defaultMessagesPageLimit = 300

// keepaliveSentinelPrefix marks cache-keepalive maintenance turns. These silent
// turns are filtered out of history before display so they (and any model
// misbehaviour on them) never reach the UI timeline.
const keepaliveSentinelPrefix = "[CACHE-KEEPALIVE]"

// ReadHistory 读取history。
func (s *service) ReadHistory(ctx context.Context, threadID string, limit int) ([]dto.Message, error) {
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return nil, err
	}
	targetID := historyTargetID(binding, threadID)
	messages, err := session.ReadHistory(ctx, targetID, util.ClampLimit(limit, 0, 0, 0))
	if err != nil {
		return nil, err
	}
	return dropKeepaliveTurns(messages), nil
}

type threadReadProvider interface {
	ReadThreadHistory(ctx context.Context, threadID string) (*ReadHistoryResult, error)
}

type runtimeConfigReaderSession interface {
	RuntimeConfigSnapshot() map[string]any
}

// ReadMessages 读取消息。
func (s *service) ReadMessages(ctx context.Context, threadID string, limit int, before string) (dto.ThreadMessagesResult, error) {
	// C1 fast-path: pending_launch threads have no binding yet, so resolveBinding
	// would fail with "no rows in result set". Return an empty result so the
	// sidebar selection + auto-history-load sequence doesn't spam errors while
	// the user is still composing their first turn.
	pendingLaunch, err := s.isThreadPendingLaunch(ctx, threadID)
	if err != nil {
		return dto.ThreadMessagesResult{}, err
	}
	if pendingLaunch {
		return dto.ThreadMessagesResult{Messages: nil, Total: 0}, nil
	}
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return dto.ThreadMessagesResult{}, err
		}
		return dto.ThreadMessagesResult{}, err
	}
	// Ensure session is resumed in background when thread is loaded after restart.
	s.backgroundResumeIfNeeded(ctx, threadID)
	agentID := agentIDFromBinding(binding, threadID)
	pageResult, err := s.readMessagesPageSource(ctx, threadID, binding, dto.MessagePageRequest{
		Limit:  normalizeMessagesPageLimit(limit),
		Before: strings.TrimSpace(before),
	})
	if err != nil {
		return dto.ThreadMessagesResult{}, err
	}
	messages := decorateThreadMessages(agentID, dropKeepaliveTurns(pageResult.Messages))
	page, err := selectMessagesPage(messages, limit, "")
	if err != nil {
		return dto.ThreadMessagesResult{}, err
	}
	total := int64(len(page))
	s.publishMessagesPage(threadID, int(total), pageCount(int(total), limit))
	return dto.ThreadMessagesResult{
		Messages:   page,
		Total:      total,
		HasMore:    pageResult.HasMore,
		NextBefore: pageResult.NextBefore,
	}, nil
}

// ReadRuntimeConfig 读取运行时配置。
func (s *service) ReadRuntimeConfig(ctx context.Context, threadID string) (map[string]any, error) {
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return s.runtimeConfigForUnresolvedSession(ctx, threadID, binding, err)
	}
	offline, offlineErr := s.buildOfflineConfig(ctx, threadID, binding)
	if offlineErr != nil {
		return nil, offlineErr
	}
	reader, ok := session.(runtimeConfigReaderSession)
	if !ok {
		return nil, errors.New("thread runtime config reader is not available")
	}
	return mergeRuntimeConfig(offline.Runtime, reader.RuntimeConfigSnapshot()), nil
}

func (s *service) runtimeConfigForUnresolvedSession(ctx context.Context, threadID string, binding *bindingstore.Binding, resolveErr error) (map[string]any, error) {
	runtimeConfig, handled, offlineErr := s.offlineRuntimeConfigForMissingSession(ctx, threadID, binding, resolveErr)
	if offlineErr != nil {
		return nil, offlineErr
	}
	if handled {
		return runtimeConfig, nil
	}
	return nil, resolveErr
}

func newThreadReadHandler(svc Service) handler.Func {
	return newThreadCall(func(ctx context.Context, id string) (any, error) {
		if reader, ok := svc.(threadReadProvider); ok {
			return reader.ReadThreadHistory(ctx, id)
		}
		return fallbackReadThreadHistory(ctx, svc, id)
	})
}

// ReadThreadHistory 读取线程history。
func (s *service) ReadThreadHistory(ctx context.Context, threadID string) (*ReadHistoryResult, error) {
	ref, err := s.Get(ctx, threadID)
	if err != nil {
		return nil, err
	}
	fallbackID := readHistoryFallbackID(ref, threadID)
	session, _, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return nil, err
	}
	threads, err := session.ListThreads(ctx)
	if err != nil {
		return nil, err
	}
	return buildReadHistoryResultFromThreads(threads, fallbackID), nil
}

// resolveBatchBinding 解析batchbinding。
func resolveBatchBinding(threadID string, thread *threadstore.Thread, allBindings []bindingstore.Binding, bindingByAgent map[string]*bindingstore.Binding) *bindingstore.Binding {
	if b, ok := bindingByAgent[threadID]; ok {
		return b
	}
	if thread != nil && thread.AgentID != "" && thread.AgentID != threadID {
		if b, ok := bindingByAgent[thread.AgentID]; ok {
			return b
		}
	}
	for i := range allBindings {
		b := allBindings[i]
		if b.ProviderThreadID == threadID || b.CodexThreadID == threadID {
			return &b
		}
	}
	return nil
}

// resolveBatchSessionCfg 解析batch会话cfg。
func (s *service) resolveBatchSessionCfg(binding *bindingstore.Binding) (map[string]any, error) {
	if binding == nil {
		return nil, nil
	}
	if s.sessions == nil {
		return nil, errors.New("session provider is not configured")
	}
	sess, err := s.sessions.GetSession(binding.AgentID)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, contract.ErrSessionNotFound
	}
	if reader, ok := sess.(runtimeConfigReaderSession); ok {
		return reader.RuntimeConfigSnapshot(), nil
	}
	return nil, errors.New("thread runtime config reader is not available")
}

// ReadRuntimeConfigs 读取运行时配置。
func (s *service) ReadRuntimeConfigs(ctx context.Context, threadIDs []string) (map[string]map[string]any, error) {
	allBindings, bindingByAgent, err := s.loadBatchBindingIndex(ctx)
	if err != nil {
		return nil, err
	}
	threadByID, err := s.loadBatchThreadIndex(ctx, threadIDs)
	if err != nil {
		return nil, err
	}

	result := make(map[string]map[string]any, len(threadIDs))
	for _, threadID := range threadIDs {
		runtime, err := s.resolveBatchRuntime(threadID, threadByID[threadID], allBindings, bindingByAgent)
		if err != nil {
			return nil, err
		}
		result[threadID] = runtime
	}

	return result, nil
}

func (s *service) loadBatchBindingIndex(ctx context.Context) ([]bindingstore.Binding, map[string]*bindingstore.Binding, error) {
	var allBindings []bindingstore.Binding
	if s.bindingStore != nil {
		var err error
		allBindings, err = s.bindingStore.ListAgentThreadBindings(ctx)
		if err != nil {
			return nil, nil, err
		}
	}
	idx := make(map[string]*bindingstore.Binding, len(allBindings))
	for i := range allBindings {
		b := allBindings[i]
		idx[b.AgentID] = &b
	}
	return allBindings, idx, nil
}

func (s *service) loadBatchThreadIndex(ctx context.Context, threadIDs []string) (map[string]*threadstore.Thread, error) {
	idx := make(map[string]*threadstore.Thread)
	if s.threadStore == nil {
		return nil, errors.New("thread store is not configured")
	}
	threads, err := s.threadStore.ListConfigsByIDs(ctx, threadIDs)
	if err != nil {
		return nil, err
	}
	for i := range threads {
		t := threads[i]
		idx[t.ThreadID] = &t
	}
	return idx, nil
}

// resolveBatchRuntime 解析batch运行时。
func (s *service) resolveBatchRuntime(
	threadID string,
	thread *threadstore.Thread,
	allBindings []bindingstore.Binding,
	bindingByAgent map[string]*bindingstore.Binding,
) (map[string]any, error) {
	binding := resolveBatchBinding(threadID, thread, allBindings, bindingByAgent)
	if thread == nil {
		return nil, fmt.Errorf("thread %q not found", threadID)
	}
	if binding == nil && strings.TrimSpace(thread.AgentID) != "" && !thread.PendingLaunch {
		return nil, fmt.Errorf("binding missing for thread %q agent %q", threadID, thread.AgentID)
	}

	var offlineCfg storedThreadConfig
	var err error
	offlineCfg, err = decodeStoredThreadConfig(offlineThreadConfigRaw(thread))
	if err != nil {
		return nil, err
	}

	baseRuntime := buildOfflineRuntimeConfig(offlineCfg, thread, binding)
	sessionCfg, err := s.resolveBatchSessionCfg(binding)
	if err != nil {
		if !errors.Is(err, contract.ErrSessionNotFound) {
			return nil, err
		}
		sessionCfg = nil
	}

	if sessionCfg != nil {
		return mergeRuntimeConfig(baseRuntime, sessionCfg), nil
	}
	return clone.RuntimeConfigMap(baseRuntime), nil
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
		return util.FirstNonEmpty(ref.ID, fallbackID)
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
		return clone.RuntimeConfigMap(overlay)
	}
	merged := clone.RuntimeConfigMap(base)
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

// dropKeepaliveTurns removes cache-keepalive maintenance turns from a history
// slice: the sentinel user message plus the assistant reply belonging to the
// same turn. Applied before decorateThreadMessages so survivors keep
// contiguous positional IDs and pagination cursors stay stable.
// dropKeepaliveTurns 去掉keepaliveturn。
func dropKeepaliveTurns(messages []dto.Message) []dto.Message {
	if len(messages) == 0 {
		return messages
	}
	out := make([]dto.Message, 0, len(messages))
	dropNextAssistant := false
	for _, msg := range messages {
		if isKeepaliveUserMessage(msg) {
			dropNextAssistant = true
			continue
		}
		if dropNextAssistant {
			dropNextAssistant = false
			if strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
				continue
			}
		}
		out = append(out, msg)
	}
	return out
}

func isKeepaliveUserMessage(msg dto.Message) bool {
	return strings.EqualFold(strings.TrimSpace(msg.Role), "user") &&
		strings.HasPrefix(strings.TrimSpace(msg.Content), keepaliveSentinelPrefix)
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

func normalizeMessagesPageLimit(limit int) int {
	if limit > 0 {
		return limit
	}
	return defaultMessagesPageLimit
}

func clampBeforeID(raw string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

// parseBeforeCursor 解析beforecursor。
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

// paginateMessagesByID 按ID处理paginate消息。
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

// Compact 处理紧凑列表。
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
	if err := s.RunPostCompactCleanup(ctx, contract.InvalidateCompact); err != nil {
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

// compactAfterTokens 处理紧凑列表后置令牌。
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

// ---------------------------------------------------------------------------
// Thread compact helpers (was compact.go)
// ---------------------------------------------------------------------------

func (s *service) publishThreadCompacted(result dto.ThreadCompactResult) {
	if s == nil || s.emitCompacted == nil || strings.TrimSpace(result.ThreadID) == "" {
		return
	}
	event := newThreadEvent(threadEventCompactedKind, result.ThreadID, threadEventFields{
		Command:      result.Command,
		BeforeTokens: result.BeforeTokens,
		AfterTokens:  result.AfterTokens,
		Compacted:    result.Compacted,
		Estimated:    result.Estimated,
	})
	if event == nil {
		return
	}
	s.emitCompacted(event.(threaddto.Compacted))
}

type transientInvalidator func(context.Context, contract.InvalidateReason) error

// RunPostCompactCleanup 运行post紧凑列表cleanup。
func (s *service) RunPostCompactCleanup(ctx context.Context, reason contract.InvalidateReason) error {
	return runTransientInvalidators(ctx, reason, s.invalidatePromptAssembly)
}

func runTransientInvalidators(
	ctx context.Context,
	reason contract.InvalidateReason,
	invalidators ...transientInvalidator,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for _, invalidator := range invalidators {
		if invalidator == nil {
			continue
		}
		if err := invalidator(ctx, reason); err != nil {
			return err
		}
	}
	return nil
}
