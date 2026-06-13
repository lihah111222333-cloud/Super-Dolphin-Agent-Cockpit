package uistate

import (
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

var uiOutputDeltaLogSampler = pkglogger.NewEverySampler(1000)

// applyAgentStateChanged 应用代理状态changed。
func (s *service) applyAgentStateChanged(ev agentdto.StateChanged) {
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	newState := strings.TrimSpace(ev.NewState)
	applyMutation(s, threadID, func() {
		// codex events use providerThreadID as ThreadID; resolve to the
		// public thread ID (agentId) so state updates match the existing sidebar entry.
		if agentID != "" {
			if resolved := s.resolveThreadIDByAgentLocked(agentID); resolved != "" {
				threadID = resolved
			}
		}
		agentState := normalizeAgentLifecycleState(newState)
		s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
			ID:      threadID,
			AgentID: agentID,
		})
		s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
			ID:         agentID,
			ThreadID:   threadID,
			State:      newState,
			AgentState: agentState,
		})
		if agentState == "idle" || agentState == "syncing" || agentState == "error" {
			s.clearThreadActivityLocked(threadID)
		}
		if strings.EqualFold(newState, "provisioning") || strings.EqualFold(newState, "starting") {
			s.setThreadOverlayLocked(threadID, overlayTypeMCPStartup, "", overlayPriorityMCPStartup, mcpStartupOverlayTTL)
		} else {
			s.clearThreadOverlayLocked(threadID, overlayTypeMCPStartup)
		}
		if agentState != "waiting" {
			s.clearThreadOverlayLocked(threadID, overlayTypeTerminalWait)
		}
		s.updateDerivedThreadStateLocked(threadID, agentID)
	}, func() uidto.UIThreadPatch {
		return s.sortedThreadPatchLocked(threadID, "agent/stateChanged")
	})
}

// applyAgentLaunched 应用代理launched。
func (s *service) applyAgentLaunched(ev agentdto.AgentLaunched) {
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	sessionID := strings.TrimSpace(ev.SessionID)
	createdAt := nonZeroTimePtr(ev.Timestamp)
	applyMutation(s, threadID, func() {
		currentState := normalizeAgentLifecycleState(firstNonEmptyString(s.agentLifecycleLocked(agentID), s.threadLifecycleLocked(threadID)))
		agentState := launchState(currentState)
		s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
			ID:        threadID,
			AgentID:   agentID,
			Name:      strings.TrimSpace(ev.Name),
			CreatedAt: createdAt,
		})
		s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
			ID:               agentID,
			ThreadID:         threadID,
			ProviderThreadID: sessionID,
			State:            agentState,
			Model:            strings.TrimSpace(ev.Model),
			CWD:              strings.TrimSpace(ev.CWD),
			LogPath:          logFilePath(),
			AgentState:       agentState,
			Name:             strings.TrimSpace(ev.Name),
			Provider:         strings.TrimSpace(ev.Provider),
			CreatedAt:        createdAt,
		})
		if currentState == "" || currentState == "starting" {
			s.setThreadOverlayLocked(threadID, overlayTypeMCPStartup, "", overlayPriorityMCPStartup, mcpStartupOverlayTTL)
		}
		s.updateDerivedThreadStateLocked(threadID, agentID)
	}, func() uidto.UIThreadPatch {
		return s.sortedThreadPatchLocked(threadID, "agent/launched")
	})
}

func (s *service) applyAgentStopped(ev agentdto.AgentStopped) {
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	applyMutation(s, threadID, func() {
		s.clearThreadActivityLocked(threadID)
		s.clearActiveTurnLocked(threadID)
		s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
			ID:      threadID,
			AgentID: agentID,
		})
		s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
			ID:         agentID,
			ThreadID:   threadID,
			State:      "stopped",
			AgentState: "idle",
		})
		s.clearThreadOverlayLocked(threadID, "")
		s.updateDerivedThreadStateLocked(threadID, agentID)
	}, func() uidto.UIThreadPatch {
		return s.sortedThreadPatchLocked(threadID, "agent/stopped")
	})
}

func (s *service) applyAgentRecovering(ev agentdto.AgentRecovering) {
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	applyMutation(s, threadID, func() {
		agentState := normalizeAgentLifecycleState("recovering")
		s.clearThreadActivityLocked(threadID)
		s.clearActiveTurnLocked(threadID)
		s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
			ID:      threadID,
			AgentID: agentID,
		})
		s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
			ID:         agentID,
			ThreadID:   threadID,
			State:      "recovering",
			AgentState: agentState,
		})
		s.clearThreadOverlayLocked(threadID, "")
		s.updateDerivedThreadStateLocked(threadID, agentID)
	}, func() uidto.UIThreadPatch {
		return s.sortedThreadPatchLocked(threadID, "agent/recovering")
	})
}

func (s *service) applyAgentFailed(ev agentdto.AgentFailed) {
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	applyMutation(s, threadID, func() {
		errText := strings.TrimSpace(ev.Error)
		s.clearThreadActivityLocked(threadID)
		s.clearActiveTurnLocked(threadID)
		s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
			ID:          threadID,
			AgentID:     agentID,
			LastMessage: errText,
		})
		s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
			ID:          agentID,
			ThreadID:    threadID,
			State:       "failed",
			AgentState:  "error",
			LastReport:  errText,
			LastMessage: errText,
		})
		s.clearThreadOverlayLocked(threadID, "")
		s.updateDerivedThreadStateLocked(threadID, agentID)
	}, func() uidto.UIThreadPatch {
		return s.sortedThreadPatchLocked(threadID, "agent/failed")
	})
}

func (s *service) applyAgentRuntimeReported(ev agentdto.AgentRuntimeReported) {
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	applyMutation(s, threadID, func() {
		s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
			ID:      threadID,
			AgentID: agentID,
		})
		s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
			ID:       agentID,
			ThreadID: threadID,
			Provider: strings.TrimSpace(ev.Provider),
			Port:     ev.Port,
		})
		s.clearThreadOverlayLocked(threadID, overlayTypeMCPStartup)
		s.updateDerivedThreadStateLocked(threadID, agentID)
	}, func() uidto.UIThreadPatch {
		return s.sortedThreadPatchLocked(threadID, "agent/runtimeReported")
	})
}

func (s *service) applyThreadStarted(ev threaddto.Started) {
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	applyMutation(s, threadID, func() {
		s.clearThreadOverlayLocked(threadID, overlayTypeMCPStartup)
		s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
			ID:      threadID,
			AgentID: agentID,
			Name:    strings.TrimSpace(ev.Name),
		})
		if agentID != "" {
			// thread.Started is a full session initialization event.
			// Unlike incremental events, all identity fields must be
			// replaced, not merged — stale values (e.g. a model leaked
			// from a different provider) must be cleared even when the
			// new value is empty.
			s.replaceAgentOnThreadStarted(agentID, AgentSummary{
				ID:               agentID,
				ThreadID:         threadID,
				ProviderThreadID: strings.TrimSpace(ev.ProviderThreadID),
				Provider:         strings.TrimSpace(ev.Provider),
				Model:            strings.TrimSpace(ev.Model),
				CWD:              strings.TrimSpace(ev.CWD),
				LogPath:          logFilePath(),
				State:            "idle",
				AgentState:       "idle",
				Name:             strings.TrimSpace(ev.Name),
			})
		}
	}, func() uidto.UIThreadPatch {
		return s.refreshThreadPatchLocked(threadID, agentID, "thread/started")
	})
}

// replaceAgentOnThreadStarted overwrites all runtime-identity fields of an
// existing AgentSummary with the values from a thread.Started event.  Unlike
// upsertAgentSummary (which uses chooseString and keeps old non-empty values),
// this resets fields that are authoritative from thread.Started — preventing a
// stale model from a previous provider from persisting across session restarts.
func (s *service) replaceAgentOnThreadStarted(agentID string, next AgentSummary) {
	for i := range s.state.Agents {
		if s.state.Agents[i].ID != agentID {
			continue
		}
		// Preserve fields that thread.Started does not own.
		next.Name = chooseString(next.Name, s.state.Agents[i].Name)
		next.ParentID = chooseString(next.ParentID, s.state.Agents[i].ParentID)
		next.LastReport = chooseString(next.LastReport, s.state.Agents[i].LastReport)
		next.LastMessage = chooseString(next.LastMessage, s.state.Agents[i].LastMessage)
		next.Port = choosePositiveInt(next.Port, s.state.Agents[i].Port)
		s.state.Agents[i] = next
		return
	}
	s.state.Agents = append(s.state.Agents, next)
}

// applyThreadUpdated 应用线程updated。
func (s *service) applyThreadUpdated(ev threaddto.Updated) {
	threadID, name := strings.TrimSpace(ev.ThreadID), strings.TrimSpace(ev.Name)
	if threadID == "" {
		return
	}
	var agentID string
	applyMutation(s, threadID, func() {
		s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{ID: threadID, Name: name})
		agentID = s.agentIDForThreadLocked(threadID)
		if ev.Model == nil || agentID == "" {
			return
		}
		model := strings.TrimSpace(*ev.Model)
		for i := range s.state.Agents {
			if s.state.Agents[i].ID == agentID {
				s.state.Agents[i].Model = model
				return
			}
		}
		if model != "" {
			s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{ID: agentID, ThreadID: threadID, Model: model})
		}
	}, func() uidto.UIThreadPatch { return s.refreshThreadPatchLocked(threadID, agentID, "thread/updated") })
	s.emitProjectionUpdatedEvents(s.projectionUpdatedLocked("sidebar"), s.projectionUpdatedLocked("state"))
}

// applyThreadStopped 应用线程stopped。
func (s *service) applyThreadStopped(ev threaddto.Stopped) {
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	status := strings.TrimSpace(ev.Status)
	if status == "" {
		status = "stopped"
	}
	deleted := strings.EqualFold(status, "deleted")
	applyMutation(s, threadID, func() {
		reason := threadStoppedLastMessage(status, ev.Reason)
		s.clearThreadActivityLocked(threadID)
		s.clearActiveTurnLocked(threadID)
		s.state.Threads = markThreadStopped(s.state.Threads, threadID, status)
		if !deleted {
			s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
				ID:          threadID,
				AgentID:     agentID,
				AgentState:  normalizeAgentLifecycleState(status),
				LastMessage: reason,
			})
		}
		if agentID != "" && !deleted {
			s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
				ID:          agentID,
				ThreadID:    threadID,
				State:       status,
				AgentState:  normalizeAgentLifecycleState(status),
				LastMessage: reason,
			})
		}
		if deleted {
			delete(s.overlayExpiryByThread, threadID)
			return
		}
		s.clearThreadOverlayLocked(threadID, "")
	}, func() uidto.UIThreadPatch {
		if deleted {
			sortThreads(s.state.Threads)
			sortAgents(s.state.Agents)
			patch := s.threadPatchLocked(threadID, "thread/stopped")
			delete(s.patchSeq, threadID)
			return patch
		}
		patch := s.refreshThreadPatchLocked(threadID, agentID, "thread/stopped")
		delete(s.patchSeq, threadID)
		return patch
	})
}

func threadStoppedLastMessage(status, reason string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error":
		return strings.TrimSpace(reason)
	default:
		return ""
	}
}

// applyTurnStarted 应用turnstarted。
func (s *service) applyTurnStarted(ev turndto.TurnStarted) {
	turnID := strings.TrimSpace(ev.TurnID)
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	duplicate := false
	applyMutation(s, threadID, func() {
		if turnID != "" && s.recentTurnExistsLocked(turnID) {
			duplicate = true
			return
		}
		startedAt := clone.Time(&ev.Timestamp)
		s.state.ActiveTurn = &TurnSummary{
			ID:        turnID,
			AgentID:   agentID,
			ThreadID:  threadID,
			Status:    "thinking",
			StartedAt: startedAt,
		}
		s.threadActivityLocked(threadID).startTurn()
		s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
			ID:      threadID,
			AgentID: agentID,
		})
		for i := range s.state.Threads {
			if s.state.Threads[i].ID == threadID {
				s.state.Threads[i].LastMessage = ""
				break
			}
		}
		if agentID != "" {
			for i := range s.state.Agents {
				if s.state.Agents[i].ID == agentID {
					s.state.Agents[i].LastMessage = ""
					break
				}
			}
		}
		s.clearThreadOverlayLocked(threadID, overlayTypeMCPStartup)
	}, func() uidto.UIThreadPatch {
		if duplicate {
			return uidto.UIThreadPatch{}
		}
		return s.refreshThreadPatchLocked(threadID, agentID, "turn/started")
	})
}

// applyTurnInterrupted 应用turninterrupted。
func (s *service) applyTurnInterrupted(ev turndto.TurnInterrupted) {
	turnID := strings.TrimSpace(ev.TurnID)
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	applyMutation(s, threadID, func() {
		startedAt := (*time.Time)(nil)
		if s.state.ActiveTurn != nil && strings.TrimSpace(s.state.ActiveTurn.ID) == turnID {
			startedAt = clone.Time(s.state.ActiveTurn.StartedAt)
			s.state.ActiveTurn = nil
		}
		if turnID != "" {
			s.state.RecentTurns = pushRecentTurn(s.state.RecentTurns, TurnSummary{
				ID:          turnID,
				AgentID:     agentID,
				ThreadID:    threadID,
				Status:      "interrupted",
				Reason:      strings.TrimSpace(ev.Reason),
				StartedAt:   startedAt,
				CompletedAt: clone.Time(&ev.Timestamp),
			}, recentTurnLimit)
		}
		s.clearThreadActivityLocked(threadID)
		s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
			ID:           threadID,
			AgentID:      agentID,
			State:        "idle",
			ThreadStatus: "idle",
		})
		s.clearThreadOverlayLocked(threadID, "")
	}, func() uidto.UIThreadPatch {
		patch := s.sortedThreadPatchLocked(threadID, "turn/interrupted")
		applyPatchStatus(&patch, "idle")
		return patch
	})
}

func (s *service) applyTurnCompleted(ev turndto.TurnCompleted) {
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	status := ""
	applyMutation(s, threadID, func() {
		completed := completedTurnSummary(s.state.ActiveTurn, ev)
		s.state.RecentTurns = pushRecentTurn(s.state.RecentTurns, completed, recentTurnLimit)
		if s.state.ActiveTurn != nil && s.state.ActiveTurn.ID == completed.ID {
			s.state.ActiveTurn = nil
		}
		status = patchStatus(completionStatus(ev))
		s.clearThreadActivityLocked(threadID)
		s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
			ID:           threadID,
			AgentID:      agentID,
			State:        status,
			ThreadStatus: status,
		})
		s.clearThreadOverlayLocked(threadID, "")
	}, func() uidto.UIThreadPatch {
		patch := s.sortedThreadPatchLocked(threadID, "turn/completed")
		applyPatchStatus(&patch, status)
		return patch
	})
}

func (s *service) applyTurnResumed(ev turndto.TurnResumed) {
	threadID, agentID := strings.TrimSpace(ev.ThreadID), strings.TrimSpace(ev.AgentID)
	applyMutation(s, threadID, func() {
		if rt := s.threadActivityLocked(threadID); rt != nil && rt.turnDepth == 0 {
			rt.turnDepth = 1
		}
	}, func() uidto.UIThreadPatch { return s.refreshThreadPatchLocked(threadID, agentID, "turn/resumed") })
}
func (s *service) applyTurnInputReceived(ev turndto.TurnInputReceived) {
	threadID, agentID := strings.TrimSpace(ev.ThreadID), strings.TrimSpace(ev.AgentID)
	applyMutation(s, threadID, func() {
		if rt := s.threadActivityLocked(threadID); rt != nil && rt.turnDepth == 0 {
			rt.turnDepth = 1
		}
	}, func() uidto.UIThreadPatch { return s.refreshThreadPatchLocked(threadID, agentID, "turn/inputReceived") })
}

// applyTurnOutputDelta 应用turnoutputdelta。
func (s *service) applyTurnOutputDelta(ev turndto.TurnOutputDelta) {
	stream, delta := strings.TrimSpace(ev.Stream), strings.TrimSpace(ev.Delta)
	if s.logger != nil && uiOutputDeltaLogSampler.ShouldLog("received:"+stream) {
		s.logger.Debug("uistate: applyTurnOutputDelta received", "sample_rate", "0.1%", "stream", stream, "thread_id", ev.ThreadID, "delta_len", len(ev.Delta))
	}
	if !strings.EqualFold(stream, "message") {
		if s.logger != nil && uiOutputDeltaLogSampler.ShouldLog("skipped:"+stream) {
			s.logger.Debug("uistate: applyTurnOutputDelta skipped", "sample_rate", "0.1%", "stream", stream, "thread_id", ev.ThreadID)
		}
		return
	}
	if delta == "" {
		return
	}
	threadID, agentID := strings.TrimSpace(ev.ThreadID), strings.TrimSpace(ev.AgentID)
	if s == nil {
		return
	}
	s.mu.Lock()
	appendLastMessageLocked(&s.state.Threads, threadID, agentID, delta)
	appendAgentMessageLocked(&s.state.Agents, agentID, threadID, delta)
	s.mu.Unlock()
}
