package uistate

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate/timeline"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
	"github.com/kelindar/event"
)

type threadPatchEmitter func(uidto.UIThreadPatch)
type preferenceChangedEmitter func(uidto.UIPreferencesChanged)
type projectionUpdatedEmitter func(uidto.UIProjectionUpdated)

func (s *service) bindDispatcher(dispatcher *event.Dispatcher) {
	if s == nil {
		return
	}
	s.emitThreadPatch = contract.NewEmitter[uidto.UIThreadPatch](dispatcher)
	s.emitPreferenceChange = contract.NewEmitter[uidto.UIPreferencesChanged](dispatcher)
	s.emitProjectionUpdated = contract.NewEmitter[uidto.UIProjectionUpdated](dispatcher)
	emitTimelineAppend := contract.NewEmitter[uidto.UITimelineAppended](dispatcher)
	if s.timeline != nil {
		s.timeline.SetEmitter(timeline.AppendedEmitter(func(ev uidto.UITimelineAppended) {
			started := time.Now()
			emitTimelineAppend(ev)
			s.recordTimelineAppendTrace(ev, time.Since(started))
		}))
	}
}

func (s *service) emitThreadPatchEvent(patch uidto.UIThreadPatch) {
	if s == nil || s.emitThreadPatch == nil || strings.TrimSpace(patch.ThreadID) == "" {
		return
	}
	started := time.Now()
	guarded := s.guardThreadPatchPayload(patch)
	s.emitThreadPatch(guarded)
	s.recordPatchEmitTrace(guarded, time.Since(started))
}

func (s *service) emitPreferenceChangedEvent(scope, key string, value any) {
	if s == nil || s.emitPreferenceChange == nil {
		return
	}
	key = normalizePreferenceKey(key)
	if key == "" {
		return
	}
	s.emitPreferenceChange(uidto.UIPreferencesChanged{
		EventHeader: sharedto.EventHeader{Timestamp: time.Now()},
		Cwd:         strings.TrimSpace(scope),
		Key:         key,
		Value:       cloneJSONValue(value),
	})
}

func (s *service) emitProjectionUpdatedEvents(events ...uidto.UIProjectionUpdated) {
	if s == nil || s.emitProjectionUpdated == nil {
		return
	}
	for _, ev := range events {
		if strings.TrimSpace(ev.Projection) == "" {
			continue
		}
		started := time.Now()
		s.emitProjectionUpdated(ev)
		s.recordProjectionUpdatedTrace(ev, time.Since(started))
	}
}

func (s *service) preferenceProjectionUpdatesLocked(key string) []uidto.UIProjectionUpdated {
	if s == nil || !shouldNotifyProjectionForPreference(key) {
		return nil
	}
	return []uidto.UIProjectionUpdated{
		s.projectionUpdatedLocked("state"),
		s.projectionUpdatedLocked("sidebar"),
	}
}

func (s *service) projectionUpdatedLocked(projection string) uidto.UIProjectionUpdated {
	projection = strings.TrimSpace(projection)
	return uidto.UIProjectionUpdated{
		UIProjectionHeader: sharedto.UIProjectionHeader{
			ThreadHeader: sharedto.ThreadHeader{
				EventHeader: sharedto.EventHeader{Timestamp: time.Now()},
			},
			Projection: projection,
		},
		Revision: s.nextProjectionRevisionLocked(projection),
	}
}

func (s *service) nextProjectionRevisionLocked(projection string) int64 {
	if s.projectionSeq == nil {
		s.projectionSeq = map[string]int64{}
	}
	s.projectionSeq[projection]++
	return s.projectionSeq[projection]
}

func shouldNotifyProjectionForPreference(key string) bool {
	key = normalizePreferenceKey(key)
	switch key {
	case preferenceActiveThreadID,
		preferenceActiveCmdThreadID,
		preferenceMainAgentID,
		preferenceViewPrefsChat,
		preferenceViewPrefsCmd,
		preferenceThreadPinsChat,
		preferenceThreadArchivesChat,
		preferenceProjectsState:
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(key), "settings.")
}

const slowUIStateTraceMS int64 = 50

func (s *service) recordPatchEmitTrace(patch uidto.UIThreadPatch, duration time.Duration) {
	if s == nil || s.trace == nil {
		return
	}
	metadata := map[string]any{
		"source":               strings.TrimSpace(patch.Source),
		"sequence":             patch.Sequence,
		"timeline_items_count": len(patch.TimelineItems),
		"removed_items_count":  len(patch.RemovedItemIds),
		"timeline_order_count": len(patch.TimelineOrder),
		"recover":              patch.Recover,
		"refresh_required":     patch.RefreshRequired,
		"fallback_reason":      strings.TrimSpace(patch.FallbackReason),
	}
	s.recordUITrace("uistate.patch.emit", strings.TrimSpace(patch.ThreadID), "", "", "", "", duration, metadata)
}

func (s *service) recordTimelineAppendTrace(ev uidto.UITimelineAppended, duration time.Duration) {
	if s == nil || s.trace == nil {
		return
	}
	metadata := map[string]any{
		"item_id":    strings.TrimSpace(ev.ItemID),
		"item_kind":  strings.TrimSpace(ev.ItemKind),
		"request_id": ev.RequestID,
	}
	s.recordUITrace("uistate.timeline.append", strings.TrimSpace(ev.ThreadID), "", strings.TrimSpace(ev.TurnID), strings.TrimSpace(ev.CallID), strings.TrimSpace(ev.ToolName), duration, metadata)
}

func (s *service) recordProjectionUpdatedTrace(ev uidto.UIProjectionUpdated, duration time.Duration) {
	if s == nil || s.trace == nil {
		return
	}
	metadata := map[string]any{
		"projection": strings.TrimSpace(ev.Projection),
		"revision":   ev.Revision,
	}
	s.recordUITrace("uistate.projection.updated", strings.TrimSpace(ev.ThreadID), "", "", "", "", duration, metadata)
}

func (s *service) recordUITrace(method, threadID, agentID, turnID, callID, toolName string, duration time.Duration, metadata map[string]any) {
	status := observability.StatusOK
	if duration.Milliseconds() >= slowUIStateTraceMS {
		status = observability.StatusSlow
	}
	event := observability.TraceEvent{
		SchemaVersion: observability.SchemaVersion,
		Timestamp:     time.Now(),
		Kind:          "ui_state",
		Method:        method,
		ThreadID:      strings.TrimSpace(threadID),
		AgentID:       strings.TrimSpace(agentID),
		TurnID:        strings.TrimSpace(turnID),
		CallID:        strings.TrimSpace(callID),
		ToolName:      strings.TrimSpace(toolName),
		DurationMS:    duration.Milliseconds(),
		Status:        status,
		Code:          observability.CodeAnchorFromCaller(0),
		Metadata:      metadata,
	}
	if err := s.trace.Record(context.Background(), event); err != nil {
		s.warnUITraceRecordFailure(event, err)
	}
}

// warnUITraceRecordFailure 把 UI trace 写失败暴露到日志，避免观测落盘故障静默。
func (s *service) warnUITraceRecordFailure(event observability.TraceEvent, err error) {
	if s == nil || s.logger == nil || err == nil {
		return
	}
	s.logger.Warn("uistate trace record failed",
		"method", event.Method,
		"thread_id", event.ThreadID,
		"turn_id", event.TurnID,
		"call_id", event.CallID,
		"status", string(event.Status),
		observability.ErrorPreviewField, observability.SafeErrorPreview(err),
		observability.ErrorCodeField, "trace_record_failed",
	)
}

// threadPatchLocked 组装单线程 UI patch，调用方必须已持有 service 锁。
// patch 只包含当前线程相关的派生字段和增量数据，超大 payload 会在发送前降级为刷新请求。
func (s *service) threadPatchLocked(threadID, source string) uidto.UIThreadPatch {
	id := strings.TrimSpace(threadID)
	if id == "" {
		return uidto.UIThreadPatch{}
	}
	// 这些字段已经属于后端 patch wire，前端可按需消费；发送端始终保持同一结构。
	patch := uidto.UIThreadPatch{
		ThreadID:          id,
		Source:            strings.TrimSpace(source),
		Sequence:          s.nextPatchSequenceLocked(id),
		ActiveThreadID:    strings.TrimSpace(s.state.ActiveThreadID),
		ActiveCmdThreadID: strings.TrimSpace(s.state.ActiveCmdThreadID),
		MainAgentID:       s.mainAgentIDLocked(),
		MainAgentState:    s.mainAgentStateLocked(),
		Partial:           true,
	}
	if summary, ok := s.threadSummaryLocked(id); ok {
		summary = s.effectiveThreadSummaryLocked(summary, time.Now())
		status, header, details := threadPatchPresentation(summary)
		patch.Thread = &uidto.ThreadPatchThread{
			ID:        summary.ID,
			Name:      summary.Name,
			State:     status,
			CreatedAt: clone.Time(summary.CreatedAt),
			UpdatedAt: clone.Time(summary.UpdatedAt),
		}
		patch.Status = status
		patch.StatusHeader = header
		patch.StatusDetails = details
		patch.OverlayText = strings.TrimSpace(summary.OverlayText)
		patch.OverlayType = strings.TrimSpace(summary.OverlayType)
		patch.OverlayPriority = summary.OverlayPriority
		patch.Interruptible = patchInterruptible(status, id, s.state.ActiveTurn)
	}
	patch.ActiveTurn = s.threadPatchActiveTurnLocked(id)
	if runtimeEntry, ok := s.threadAgentRuntimeLocked(id); ok {
		patch.AgentRuntime = runtimeEntry
	}
	if agentMeta := s.threadPatchAgentMetaLocked(id); len(agentMeta) > 0 {
		patch.AgentMeta = agentMeta
	}
	s.applyThreadTimelineLocked(&patch, id)
	s.applyThreadActivityStatsLocked(&patch, id)
	s.applyThreadDiffLocked(&patch, id, source)
	return patch
}

func (s *service) threadPatchActiveTurnLocked(threadID string) *uidto.ThreadPatchActiveTurn {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || s.state.ActiveTurn == nil {
		return nil
	}
	turn := s.state.ActiveTurn
	if strings.TrimSpace(turn.ID) == "" || strings.TrimSpace(turn.ThreadID) != threadID {
		return nil
	}
	return &uidto.ThreadPatchActiveTurn{
		ID:          strings.TrimSpace(turn.ID),
		ThreadID:    threadID,
		AgentID:     strings.TrimSpace(turn.AgentID),
		Status:      strings.TrimSpace(turn.Status),
		StartedAt:   clone.Time(turn.StartedAt),
		CompletedAt: clone.Time(turn.CompletedAt),
	}
}

func (s *service) threadPatchAgentMetaLocked(threadID string) map[string]any {
	recentByThread := latestTurnsByThread(s.state.ActiveTurn, s.state.RecentTurns)
	turn, ok := recentByThread[strings.TrimSpace(threadID)]
	if !ok {
		return nil
	}
	ts := recentTurnTime(turn)
	if ts.IsZero() {
		return nil
	}
	return map[string]any{"lastActiveAt": ts.UTC().Format(time.RFC3339Nano)}
}

func (s *service) threadAgentRuntimeLocked(threadID string) (map[string]any, bool) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, false
	}
	for _, agent := range s.state.Agents {
		if strings.TrimSpace(agent.ThreadID) != threadID {
			continue
		}
		agentID := strings.TrimSpace(agent.ID)
		if agentID == "" {
			return nil, false
		}
		return buildAgentRuntimeEntry(&agent, agentID, threadID), true
	}
	return nil, false
}

func (s *service) nextPatchSequenceLocked(threadID string) int64 {
	if s.patchSeq == nil {
		s.patchSeq = map[string]int64{}
	}
	s.patchSeq[threadID]++
	return s.patchSeq[threadID]
}

const threadPatchMaxPayloadBytes = 64 * 1024

// applyThreadDiffLocked 将当前 diff 文本或恢复提示写入线程 patch。
// diff 被清空但 revision 前进时要求前端刷新，避免继续展示旧 diff。
func (s *service) applyThreadDiffLocked(patch *uidto.UIThreadPatch, threadID, source string) {
	if patch == nil {
		return
	}
	revision := s.currentDiffRevisionLocked(threadID)
	if revision > 0 {
		patch.DiffRevision = revision
	}
	diffText := s.currentDiffTextLocked(threadID)
	if diffText != "" {
		patch.DiffText = diffText
		return
	}
	if revision > 0 && strings.TrimSpace(source) == "tool/diffUpdated" {
		patch.Recover = true
		patch.RefreshRequired = true
		patch.FallbackReason = "diff_cleared"
	}
}

func patchInterruptible(status, threadID string, activeTurn *TurnSummary) *bool {
	status = strings.TrimSpace(status)
	if status == "" {
		return nil
	}
	interruptible := activeTurn != nil &&
		strings.TrimSpace(activeTurn.ID) != "" &&
		strings.TrimSpace(activeTurn.ThreadID) == strings.TrimSpace(threadID) &&
		sidebarInterruptible(status)
	return &interruptible
}

func (s *service) guardThreadPatchPayload(patch uidto.UIThreadPatch) uidto.UIThreadPatch {
	payload, err := json.Marshal(patch)
	if err != nil || len(payload) <= threadPatchMaxPayloadBytes {
		return patch
	}
	return uidto.UIThreadPatch{
		ThreadID:        patch.ThreadID,
		Source:          patch.Source,
		Sequence:        patch.Sequence,
		Status:          patch.Status,
		StatusHeader:    patch.StatusHeader,
		StatusDetails:   patch.StatusDetails,
		Interruptible:   patch.Interruptible,
		ActiveTurn:      patch.ActiveTurn,
		Recover:         true,
		RefreshRequired: true,
		FallbackReason:  "payload_too_large",
	}
}

func (s *service) threadSummaryLocked(threadID string) (ThreadSummary, bool) {
	for _, item := range s.state.Threads {
		if item.ID == threadID {
			return item, true
		}
	}
	return ThreadSummary{}, false
}

// eventThreadActivityLocked 解析事件线程活动记录，缺少 threadID 或活动状态时记录告警并跳过。
// 返回的 threadActivity 只在调用方持锁期间有效。
func (s *service) eventThreadActivityLocked(threadID, agentID, source string) (string, *threadActivity, bool) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		if s != nil && s.logger != nil {
			s.logger.Warn(
				"uistate: skip activity event without thread id",
				"source", strings.TrimSpace(source),
				"agent_id", strings.TrimSpace(agentID),
			)
		}
		return "", nil, false
	}
	rt := s.threadActivityLocked(threadID)
	if rt == nil {
		if s != nil && s.logger != nil {
			s.logger.Warn(
				"uistate: skip activity event without thread state",
				"source", strings.TrimSpace(source),
				"thread_id", threadID,
				"agent_id", strings.TrimSpace(agentID),
			)
		}
		return "", nil, false
	}
	return threadID, rt, true
}

func (s *service) mainAgentIDLocked() string {
	if current := strings.TrimSpace(s.state.MainAgentID); current != "" {
		return current
	}
	return strings.TrimSpace(deriveMainAgentID(s.state.Agents, ""))
}

// mainAgentStateLocked 从主 agent 对应线程和 agent 摘要中推导 patch 使用的主状态。
func (s *service) mainAgentStateLocked() string {
	mainAgentID := s.mainAgentIDLocked()
	if mainAgentID == "" {
		return ""
	}
	for _, agent := range s.state.Agents {
		if strings.TrimSpace(agent.ID) == mainAgentID {
			if summary, ok := s.threadSummaryLocked(agent.ThreadID); ok {
				return patchStatus(firstNonEmptyString(
					summary.ThreadStatus,
					summary.AgentState,
					agent.ThreadStatus,
					agent.AgentState,
					agent.State,
				))
			}
			return patchStatus(firstNonEmptyString(agent.ThreadStatus, agent.AgentState, agent.State))
		}
	}
	return ""
}

// applyRuntimePreferenceLocked 将会影响 UI 运行态的偏好同步到内存状态。
// 调用方负责持锁和持久化；这里只做字段投影，不写 store。
func (s *service) applyRuntimePreferenceLocked(key string, value any) {
	switch normalizePreferenceKey(key) {
	case preferenceActiveThreadID:
		s.state.ActiveThreadID = preferenceString(value)
	case preferenceActiveCmdThreadID:
		s.state.ActiveCmdThreadID = preferenceString(value)
	case preferenceMainAgentID:
		s.state.MainAgentID = strings.TrimSpace(deriveMainAgentID(s.state.Agents, preferenceString(value)))
	case preferenceStallThresholdSec:
		if sec := asPositiveInt(value, 30); sec > 0 {
			s.state.StallThresholdSec = sec
		}
	case preferenceShowInjectedPromptInChat:
		s.state.ShowInjectedPromptInChat = boolPreferencePointer(value, false)
	}
}

func preferenceString(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func threadPatchPresentation(summary ThreadSummary) (string, string, string) {
	if status, header, details, ok := sidebarThreadOverlay(&summary); ok {
		return status, header, details
	}
	if status, ok := lifecycleSidebarStatus(summary.LifecycleStatus); ok {
		header, details := sidebarStatusText(status, summary.LastMessage)
		return status, header, details
	}
	status := patchStatus(firstNonEmptyString(summary.ThreadStatus, summary.State))
	header, details := sidebarStatusText(status, summary.LastMessage)
	return status, header, details
}

func applyPatchStatus(patch *uidto.UIThreadPatch, status string) {
	if patch == nil {
		return
	}
	if status = patchStatus(status); status == "" {
		return
	}
	patch.Status = status
	if patch.Thread == nil {
		patch.Thread = &uidto.ThreadPatchThread{ID: patch.ThreadID, Name: patch.ThreadID}
	}
	patch.Thread.State = status
}

func tokenUsagePatch(ev uidto.UITokensUpdated) *uidto.ThreadPatchTokenUsage {
	usage := &uidto.ThreadPatchTokenUsage{
		UsedTokens:          ev.TotalTokens,
		ContextWindowTokens: ev.ContextWindowTokens,
	}
	if usage.ContextWindowTokens > 0 {
		usage.UsedPercent = float64(usage.UsedTokens) * 100 / float64(usage.ContextWindowTokens)
	}
	return usage
}

func patchStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "starting", "thinking", "responding", "running", "editing", "waiting", "syncing", "archived":
		return strings.ToLower(strings.TrimSpace(status))
	case "error", "failed":
		return "error"
	case "":
		return ""
	default:
		return "idle"
	}
}
