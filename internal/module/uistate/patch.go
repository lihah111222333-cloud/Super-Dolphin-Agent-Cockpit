package uistate

import (
	"strings"
	"time"

	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/kelindar/event"
)

type threadPatchEmitter func(uidto.UIThreadPatch)
type preferenceChangedEmitter func(uidto.UIPreferencesChanged)
type projectionUpdatedEmitter func(uidto.UIProjectionUpdated)

func (s *service) bindDispatcher(dispatcher *event.Dispatcher) {
	if s == nil {
		return
	}
	s.emitThreadPatch = bus.NewEmitter[uidto.UIThreadPatch](dispatcher)
	s.emitPreferenceChange = bus.NewEmitter[uidto.UIPreferencesChanged](dispatcher)
	s.emitProjectionUpdated = bus.NewEmitter[uidto.UIProjectionUpdated](dispatcher)
}

func (s *service) emitThreadPatchEvent(patch uidto.UIThreadPatch) {
	if s == nil || s.emitThreadPatch == nil || strings.TrimSpace(patch.ThreadID) == "" {
		return
	}
	s.emitThreadPatch(patch)
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
		patch.Thread = &uidto.ThreadPatchThread{
			ID:    summary.ID,
			Name:  summary.Name,
			State: patchStatus(summary.State),
		}
		if summary.State != "" {
			patch.Status = patchStatus(summary.State)
		}
	}
	return patch
}

func (s *service) nextPatchSequenceLocked(threadID string) int64 {
	if s.patchSeq == nil {
		s.patchSeq = map[string]int64{}
	}
	s.patchSeq[threadID]++
	return s.patchSeq[threadID]
}

func (s *service) currentDiffRevisionLocked(threadID string) int64 {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || s.projectionSeq == nil {
		return 0
	}
	return s.projectionSeq["diff:"+threadID]
}

func (s *service) bumpDiffRevisionLocked(threadID string) int64 {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return 0
	}
	return s.nextProjectionRevisionLocked("diff:" + threadID)
}

func (s *service) threadSummaryLocked(threadID string) (ThreadSummary, bool) {
	for _, item := range s.state.Threads {
		if item.ID == threadID {
			return item, true
		}
	}
	return ThreadSummary{}, false
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
			return patchStatus(agent.State)
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
	}
}

func preferenceString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
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
