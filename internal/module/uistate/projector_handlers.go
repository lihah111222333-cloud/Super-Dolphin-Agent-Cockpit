package uistate

import (
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

func (s *service) applyAgentStateChanged(ev agentdto.StateChanged) {
	s.mu.Lock()
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	newState := strings.TrimSpace(ev.NewState)
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
	s.updateDerivedThreadStateLocked(threadID, agentID)
	sortThreads(s.state.Threads)
	sortAgents(s.state.Agents)
	patch := s.threadPatchLocked(threadID, "agent/stateChanged")
	s.mu.Unlock()
	s.emitThreadPatchEvent(patch)
}

func (s *service) applyAgentLaunched(ev agentdto.AgentLaunched) {
	s.mu.Lock()
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	currentState := normalizeAgentLifecycleState(firstNonEmptyString(
		s.agentStateLocked(agentID),
		s.threadAgentStateLocked(threadID),
	))
	agentState := launchedAgentState(currentState)
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
	sortThreads(s.state.Threads)
	sortAgents(s.state.Agents)
	patch := s.threadPatchLocked(threadID, "agent/launched")
	s.mu.Unlock()
	s.emitThreadPatchEvent(patch)
}

func (s *service) applyAgentStopped(ev agentdto.AgentStopped) {
	s.mu.Lock()
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
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
	s.clearThreadOverlayLocked(threadID, overlayTypeMCPStartup)
	s.updateDerivedThreadStateLocked(threadID, agentID)
	sortThreads(s.state.Threads)
	sortAgents(s.state.Agents)
	patch := s.threadPatchLocked(threadID, "agent/stopped")
	s.mu.Unlock()
	s.emitThreadPatchEvent(patch)
}

func (s *service) applyAgentRecovering(ev agentdto.AgentRecovering) {
	s.mu.Lock()
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
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
	s.clearThreadOverlayLocked(threadID, overlayTypeMCPStartup)
	s.updateDerivedThreadStateLocked(threadID, agentID)
	sortThreads(s.state.Threads)
	sortAgents(s.state.Agents)
	patch := s.threadPatchLocked(threadID, "agent/recovering")
	s.mu.Unlock()
	s.emitThreadPatchEvent(patch)
}

func (s *service) applyAgentFailed(ev agentdto.AgentFailed) {
	s.mu.Lock()
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	s.clearThreadActivityLocked(threadID)
	s.clearActiveTurnLocked(threadID)
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:          threadID,
		AgentID:     agentID,
		LastMessage: strings.TrimSpace(ev.Error),
	})
	s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
		ID:          agentID,
		ThreadID:    threadID,
		State:       "failed",
		AgentState:  "error",
		LastReport:  strings.TrimSpace(ev.Error),
		LastMessage: strings.TrimSpace(ev.Error),
	})
	s.clearThreadOverlayLocked(threadID, overlayTypeMCPStartup)
	s.updateDerivedThreadStateLocked(threadID, agentID)
	sortThreads(s.state.Threads)
	sortAgents(s.state.Agents)
	patch := s.threadPatchLocked(threadID, "agent/failed")
	s.mu.Unlock()
	s.emitThreadPatchEvent(patch)
}

func (s *service) applyAgentRuntimeReported(ev agentdto.AgentRuntimeReported) {
	s.mu.Lock()
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
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
	sortThreads(s.state.Threads)
	sortAgents(s.state.Agents)
	patch := s.threadPatchLocked(threadID, "agent/runtimeReported")
	s.mu.Unlock()
	s.emitThreadPatchEvent(patch)
}

func (s *service) applyThreadStarted(ev threaddto.Started) {
	s.mu.Lock()
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	s.clearThreadOverlayLocked(threadID, overlayTypeMCPStartup)
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:      threadID,
		AgentID: agentID,
	})
	patch := s.refreshThreadPatchLocked(threadID, agentID, "thread/started")
	s.mu.Unlock()
	s.emitThreadPatchEvent(patch)
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
	s.clearThreadOverlayLocked(threadID, overlayTypeMCPStartup)
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
	if turnID != "" && s.hasRecentTurnLocked(turnID) {
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
	s.clearThreadOverlayLocked(threadID, overlayTypeMCPStartup)
	sortThreads(s.state.Threads)
	patch := s.threadPatchLocked(threadID, "turn/interrupted")
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
	s.clearThreadOverlayLocked(threadID, overlayTypeMCPStartup)
	sortThreads(s.state.Threads)
	patch := s.threadPatchLocked(threadID, "turn/completed")
	applyPatchStatus(&patch, status)
	s.mu.Unlock()
	s.emitThreadPatchEvent(patch)
}

func (s *service) applyTurnResumed(ev turndto.TurnResumed) {
	s.mu.Lock()
	rt := s.threadActivityLocked(ev.ThreadID)
	if rt != nil && rt.turnDepth == 0 {
		rt.turnDepth = 1
	}
	patch := s.refreshThreadPatchLocked(ev.ThreadID, ev.AgentID, "turn/resumed")
	s.mu.Unlock()
	s.emitThreadPatchEvent(patch)
}

func (s *service) applyTurnInputReceived(ev turndto.TurnInputReceived) {
	s.mu.Lock()
	rt := s.threadActivityLocked(ev.ThreadID)
	if rt != nil && rt.turnDepth == 0 {
		rt.turnDepth = 1
	}
	patch := s.refreshThreadPatchLocked(ev.ThreadID, ev.AgentID, "turn/inputReceived")
	s.mu.Unlock()
	s.emitThreadPatchEvent(patch)
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

func (s *service) threadAgentStateLocked(threadID string) string {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}
	for _, item := range s.state.Threads {
		if item.ID == threadID {
			return normalizeAgentLifecycleState(firstNonEmptyString(item.AgentState, item.ThreadStatus, item.State))
		}
	}
	return ""
}

func (s *service) agentStateLocked(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ""
	}
	for _, item := range s.state.Agents {
		if item.ID == agentID {
			return normalizeAgentLifecycleState(firstNonEmptyString(item.AgentState, item.ThreadStatus, item.State))
		}
	}
	return ""
}

func (s *service) hasRecentTurnLocked(turnID string) bool {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return false
	}
	for _, item := range s.state.RecentTurns {
		if item.ID == turnID {
			return true
		}
	}
	return false
}

func launchedAgentState(current string) string {
	current = strings.TrimSpace(current)
	if current == "" || strings.EqualFold(current, "starting") {
		return "starting"
	}
	return current
}
