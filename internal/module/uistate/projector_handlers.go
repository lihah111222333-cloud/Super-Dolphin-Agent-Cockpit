package uistate

import (
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
)

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

func (s *service) applyAgentLaunched(ev agentdto.AgentLaunched) {
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	applyMutation(s, threadID, func() {
		currentState := normalizeAgentLifecycleState(firstNonEmptyString(s.agentLifecycleLocked(agentID), s.threadLifecycleLocked(threadID)))
		agentState := launchState(currentState)
		s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
			ID:      threadID,
			AgentID: agentID,
		})
		s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
			ID:         agentID,
			ThreadID:   threadID,
			State:      agentState,
			CWD:        strings.TrimSpace(ev.CWD),
			AgentState: agentState,
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
		})
		if agentID != "" {
			s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
				ID:               agentID,
				ThreadID:         threadID,
				ProviderThreadID: strings.TrimSpace(ev.ProviderThreadID),
				Provider:         strings.TrimSpace(ev.Provider),
				Model:            strings.TrimSpace(ev.Model),
				CWD:              strings.TrimSpace(ev.CWD),
				LogPath:          logFilePath(),
				State:            "idle",
				AgentState:       "idle",
			})
		}
	}, func() uidto.UIThreadPatch {
		return s.refreshThreadPatchLocked(threadID, agentID, "thread/started")
	})
}

func (s *service) applyThreadStopped(ev threaddto.Stopped) {
	s.mu.Lock()
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	status := strings.TrimSpace(ev.Status)
	if status == "" {
		status = "stopped"
	}
	s.clearThreadActivityLocked(threadID)
	s.clearActiveTurnLocked(threadID)
	s.state.Threads = markThreadStopped(s.state.Threads, threadID, status)
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:         threadID,
		AgentID:    agentID,
		AgentState: normalizeAgentLifecycleState(status),
	})
	if agentID != "" {
		s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
			ID:         agentID,
			ThreadID:   threadID,
			State:      status,
			AgentState: normalizeAgentLifecycleState(status),
		})
	}
	s.clearThreadOverlayLocked(threadID, "")
	patch := s.refreshThreadPatchLocked(threadID, agentID, "thread/stopped")
	delete(s.patchSeq, threadID)
	s.mu.Unlock()
	s.emitThreadPatchEvent(patch)
}

func (s *service) applyTurnStarted(ev turndto.TurnStarted) {
	s.mu.Lock()
	turnID := strings.TrimSpace(ev.TurnID)
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	if turnID != "" && s.recentTurnExistsLocked(turnID) {
		s.mu.Unlock()
		return
	}
	startedAt := cloneTime(&ev.Timestamp)
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
	s.clearThreadOverlayLocked(threadID, overlayTypeMCPStartup)
	patch := s.refreshThreadPatchLocked(threadID, agentID, "turn/started")
	s.mu.Unlock()
	s.emitThreadPatchEvent(patch)
}

func (s *service) applyTurnInterrupted(ev turndto.TurnInterrupted) {
	s.mu.Lock()
	turnID := strings.TrimSpace(ev.TurnID)
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	startedAt := (*time.Time)(nil)
	if s.state.ActiveTurn != nil && strings.TrimSpace(s.state.ActiveTurn.ID) == turnID {
		startedAt = cloneTime(s.state.ActiveTurn.StartedAt)
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
			CompletedAt: cloneTime(&ev.Timestamp),
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
	patch := s.sortedThreadPatchLocked(threadID, "turn/interrupted")
	applyPatchStatus(&patch, "idle")
	s.mu.Unlock()
	s.emitThreadPatchEvent(patch)
}

func (s *service) applyTurnCompleted(ev turndto.TurnCompleted) {
	s.mu.Lock()
	completed := completedTurnSummary(s.state.ActiveTurn, ev)
	s.state.RecentTurns = pushRecentTurn(s.state.RecentTurns, completed, recentTurnLimit)
	if s.state.ActiveTurn != nil && s.state.ActiveTurn.ID == completed.ID {
		s.state.ActiveTurn = nil
	}
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	status := patchStatus(completionStatus(ev))
	s.clearThreadActivityLocked(threadID)
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:           threadID,
		AgentID:      agentID,
		State:        status,
		ThreadStatus: status,
	})
	s.clearThreadOverlayLocked(threadID, "")
	patch := s.sortedThreadPatchLocked(threadID, "turn/completed")
	applyPatchStatus(&patch, status)
	s.mu.Unlock()
	s.emitThreadPatchEvent(patch)
}

func (s *service) applyTurnResumed(ev turndto.TurnResumed) {
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	applyMutation(s, threadID, func() {
		rt := s.threadActivityLocked(threadID)
		if rt != nil && rt.turnDepth == 0 {
			rt.turnDepth = 1
		}
	}, func() uidto.UIThreadPatch {
		return s.refreshThreadPatchLocked(threadID, agentID, "turn/resumed")
	})
}

func (s *service) applyTurnInputReceived(ev turndto.TurnInputReceived) {
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	applyMutation(s, threadID, func() {
		rt := s.threadActivityLocked(threadID)
		if rt != nil && rt.turnDepth == 0 {
			rt.turnDepth = 1
		}
	}, func() uidto.UIThreadPatch {
		return s.refreshThreadPatchLocked(threadID, agentID, "turn/inputReceived")
	})
}

func (s *service) applyTurnOutputDelta(ev turndto.TurnOutputDelta) {
	if !strings.EqualFold(strings.TrimSpace(ev.Stream), "message") {
		return
	}
	delta := strings.TrimSpace(ev.Delta)
	if delta == "" {
		return
	}
	s.mu.Lock()
	appendLastMessageLocked(&s.state.Threads, strings.TrimSpace(ev.ThreadID), strings.TrimSpace(ev.AgentID), delta)
	appendAgentMessageLocked(&s.state.Agents, strings.TrimSpace(ev.AgentID), strings.TrimSpace(ev.ThreadID), delta)
	s.mu.Unlock()
}
