package uistate

import (
	"encoding/json"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"strings"
	"time"

	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate/timeline"
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
		s.timeline.SetEmitter(timeline.AppendedEmitter(emitTimelineAppend))
	}
}

func (s *service) emitThreadPatchEvent(patch uidto.UIThreadPatch) {
	if s == nil || s.emitThreadPatch == nil || strings.TrimSpace(patch.ThreadID) == "" {
		return
	}
	s.emitThreadPatch(s.guardThreadPatchPayload(patch))
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
		s.emitProjectionUpdated(ev)
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

func (s *service) threadPatchLocked(threadID, source string) uidto.UIThreadPatch {
	id := strings.TrimSpace(threadID)
	if id == "" {
		return uidto.UIThreadPatch{}
	}
	// Forward-compatible fields. The backend publishes them now; frontend
	// thread-live-patch.js can start consuming activeThreadId/mainAgentId/partial
	// once that bridge path is upgraded.
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
			ID:    summary.ID,
			Name:  summary.Name,
			State: status,
		}
		patch.Status = status
		patch.StatusHeader = header
		patch.StatusDetails = details
		patch.OverlayText = strings.TrimSpace(summary.OverlayText)
		patch.OverlayType = strings.TrimSpace(summary.OverlayType)
		patch.OverlayPriority = summary.OverlayPriority
		patch.Interruptible = patchInterruptible(status)
	}
	s.applyThreadTimelineLocked(&patch, id)
	s.applyThreadActivityStatsLocked(&patch, id)
	s.applyThreadDiffLocked(&patch, id, source)
	return patch
}

func (s *service) nextPatchSequenceLocked(threadID string) int64 {
	if s.patchSeq == nil {
		s.patchSeq = map[string]int64{}
	}
	s.patchSeq[threadID]++
	return s.patchSeq[threadID]
}

const threadPatchMaxPayloadBytes = 64 * 1024

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

func patchInterruptible(status string) *bool {
	status = strings.TrimSpace(status)
	if status == "" {
		return nil
	}
	interruptible := sidebarInterruptible(status)
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
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func threadPatchPresentation(summary ThreadSummary) (string, string, string) {
	if status, header, details, ok := sidebarThreadOverlay(&summary); ok {
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
	case "starting", "thinking", "responding", "running", "editing", "waiting", "syncing":
		return strings.ToLower(strings.TrimSpace(status))
	case "error", "failed":
		return "error"
	case "":
		return ""
	default:
		return "idle"
	}
}
