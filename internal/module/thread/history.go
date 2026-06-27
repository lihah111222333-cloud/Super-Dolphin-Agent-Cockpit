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
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
)

const eventTypeAgentMessage = "agent_message"
const defaultMessagesPageLimit = 300

// keepaliveSentinelPrefix 标记缓存保活用的维护 turn。
// 展示历史前会过滤这些静默 turn，避免维护消息和模型回复进入 UI 时间线。
const keepaliveSentinelPrefix = "[CACHE-KEEPALIVE]"

// ReadHistory 从当前 provider session 读取线程历史。
// 返回前会过滤 keepalive 维护 turn；session/binding 解析失败直接返回错误，不读取离线文件。
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

// ReadMessages 返回 UI 分页消息列表。
// pending_launch 线程尚无 binding/provider，会返回空页；普通线程会优先恢复 session，再按 provider 或 jsonl 历史分页。
func (s *service) ReadMessages(ctx context.Context, threadID string, limit int, before string) (dto.ThreadMessagesResult, error) {
	// pending_launch 线程尚未写入 binding，按空页返回可让侧边栏自动加载保持安静。
	// 真正的 provider 历史会在首轮 SpawnIfNeeded 持久化 binding 后再读取。
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
	// 线程在重启后被打开时尝试后台恢复 session，避免用户下一条消息才触发冷启动。
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

// ReadRuntimeConfig 读取线程当前可见的 runtime config。
// 活跃 session 的快照会覆盖离线持久化配置；session 缺失时只在可定位 binding 的情况下走离线路径。
func (s *service) ReadRuntimeConfig(ctx context.Context, threadID string) (map[string]any, error) {
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		runtimeConfig, handled, offlineErr := s.offlineRuntimeConfigForMissingSession(ctx, threadID, binding, err)
		if offlineErr != nil {
			return nil, offlineErr
		}
		if handled {
			return runtimeConfig, nil
		}
		return nil, err
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

func newThreadReadHandler(svc Service) handler.Func {
	return newThreadCall(func(ctx context.Context, id string) (any, error) {
		if reader, ok := svc.(threadReadProvider); ok {
			return reader.ReadThreadHistory(ctx, id)
		}
		return fallbackReadThreadHistory(ctx, svc, id)
	})
}

// ReadThreadHistory 读取 provider 侧可见的历史 thread 列表。
// provider 不返回列表时至少返回当前 thread id，保持旧 UI 的 history 面板不空白。
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

// resolveBatchBinding 在批量 runtime config 查询中为 thread 匹配 binding。
// 优先使用 thread/agent id 直接索引，再退回 provider thread id，避免逐条访问 binding store。
func resolveBatchBinding(threadID string, thread *threadConfigRecord, allBindings []threadBindingRecord, bindingByAgent map[string]*threadBindingRecord) *threadBindingRecord {
	if b, ok := bindingByAgent[threadID]; ok {
		return b
	}
	if thread != nil && thread.AgentID != "" && thread.AgentID != threadID {
		if b, ok := bindingByAgent[thread.AgentID]; ok {
			return b
		}
	}
	for i := range allBindings {
		if allBindings[i].ProviderThreadID == threadID || allBindings[i].CodexThreadID == threadID {
			return &allBindings[i]
		}
	}
	return nil
}

// resolveBatchSessionCfg 读取批量路径中的活跃 session runtime 快照。
// session provider 未装配或 session 不支持快照接口时返回错误，让调用方决定是否仅使用离线配置。
func (s *service) resolveBatchSessionCfg(binding *threadBindingRecord) (map[string]any, error) {
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

// ReadRuntimeConfigs 批量读取多个线程的 runtime config。
// 先加载 thread/binding 索引，再逐个合并离线配置和活跃 session 快照，避免 N 次 binding store 查询。
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

func (s *service) loadBatchBindingIndex(ctx context.Context) ([]threadBindingRecord, map[string]*threadBindingRecord, error) {
	var allBindings []threadBindingRecord
	store := s.threadBindingStorePort()
	if store != nil {
		var err error
		allBindings, err = store.ListAgentThreadBindings(ctx)
		if err != nil {
			return nil, nil, err
		}
	}
	idx := make(map[string]*threadBindingRecord, len(allBindings))
	for i := range allBindings {
		idx[allBindings[i].AgentID] = &allBindings[i]
	}
	return allBindings, idx, nil
}

func (s *service) loadBatchThreadIndex(ctx context.Context, threadIDs []string) (map[string]*threadConfigRecord, error) {
	idx := make(map[string]*threadConfigRecord)
	store := s.threadConfigStorePort()
	if store == nil {
		return nil, errors.New("thread store is not configured")
	}
	threads, err := store.ListConfigsByIDs(ctx, threadIDs)
	if err != nil {
		return nil, err
	}
	for i := range threads {
		idx[threads[i].ThreadID] = &threads[i]
	}
	return idx, nil
}

// resolveBatchRuntime 合成单个线程的批量 runtime config。
// 非 pending 线程缺 binding 会报错；活跃 session 缺失时保留离线配置，其他 session 错误直接返回。
func (s *service) resolveBatchRuntime(
	threadID string,
	thread *threadConfigRecord,
	allBindings []threadBindingRecord,
	bindingByAgent map[string]*threadBindingRecord,
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
	offlineCfg, err = decodeStoredThreadConfig(batchThreadConfigRaw(thread))
	if err != nil {
		return nil, err
	}

	baseRuntime := buildBatchOfflineRuntimeConfig(offlineCfg, thread, binding)
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

// dropKeepaliveTurns 从历史中移除缓存保活 turn。
// 它同时丢弃 sentinel user 消息和紧随其后的 assistant 回复，后续再补连续 ID，确保分页 cursor 稳定。
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

// parseBeforeCursor 解析消息分页 before cursor。
// 支持 RFC3339/RFC3339Nano 以及秒、毫秒、微秒、纳秒时间戳；非法值直接返回参数错误。
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

// paginateMessagesByID 按消息 ID 倒序取一页。
// before 为 0 表示取最新页；返回顺序保持从新到旧，匹配现有 UI 消费方式。
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

// Compact 调用 provider 压缩当前线程上下文，并发布 compact 事件。
// provider 不支持时返回友好的 capability 错误；压缩后会触发 prompt assembly 清理，防止旧上下文继续参与后续 turn。
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

// compactAfterTokens 在压缩后短轮询估算 token 数。
// provider 压缩可能异步落盘，最多轮询三次；仍未下降时返回最后一次估算值供 UI 展示。
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
// thread 紧凑辅助函数
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

// RunPostCompactCleanup 在紧凑完成后清理 transient prompt 缓存。
// cleanup 失败会向调用方返回错误，避免继续使用已经失效的提示词组合。
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
