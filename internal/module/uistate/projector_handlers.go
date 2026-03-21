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
	defer s.mu.Unlock()
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:         strings.TrimSpace(ev.ThreadID),
		AgentID:    strings.TrimSpace(ev.AgentID),
		AgentState: strings.TrimSpace(ev.NewState),
	})
	s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
		ID:         strings.TrimSpace(ev.AgentID),
		ThreadID:   strings.TrimSpace(ev.ThreadID),
		State:      strings.TrimSpace(ev.NewState),
		AgentState: strings.TrimSpace(ev.NewState),
	})
	sortThreads(s.state.Threads)
	sortAgents(s.state.Agents)
}

func (s *service) applyAgentLaunched(ev agentdto.AgentLaunched) {
	s.mu.Lock()
	defer s.mu.Unlock()
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	agentState := launchedAgentState(firstNonEmptyString(
		s.threadAgentStateLocked(threadID),
		s.agentStateLocked(agentID),
	))
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:         threadID,
		AgentID:    agentID,
		AgentState: agentState,
	})
	s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
		ID:         agentID,
		ThreadID:   threadID,
		State:      agentState,
		CWD:        strings.TrimSpace(ev.CWD),
		AgentState: agentState,
	})
	sortThreads(s.state.Threads)
	sortAgents(s.state.Agents)
}

func (s *service) applyAgentStopped(ev agentdto.AgentStopped) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:         strings.TrimSpace(ev.ThreadID),
		AgentID:    strings.TrimSpace(ev.AgentID),
		AgentState: "stopped",
	})
	s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
		ID:         strings.TrimSpace(ev.AgentID),
		ThreadID:   strings.TrimSpace(ev.ThreadID),
		State:      "stopped",
		AgentState: "stopped",
	})
	sortThreads(s.state.Threads)
	sortAgents(s.state.Agents)
}

func (s *service) applyAgentRecovering(ev agentdto.AgentRecovering) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:         strings.TrimSpace(ev.ThreadID),
		AgentID:    strings.TrimSpace(ev.AgentID),
		AgentState: "recovering",
	})
	s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
		ID:         strings.TrimSpace(ev.AgentID),
		ThreadID:   strings.TrimSpace(ev.ThreadID),
		State:      "recovering",
		AgentState: "recovering",
	})
	sortThreads(s.state.Threads)
	sortAgents(s.state.Agents)
}

func (s *service) applyAgentFailed(ev agentdto.AgentFailed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:           strings.TrimSpace(ev.ThreadID),
		AgentID:      strings.TrimSpace(ev.AgentID),
		State:        "error",
		ThreadStatus: "error",
		AgentState:   "error",
		LastMessage:  strings.TrimSpace(ev.Error),
	})
	s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
		ID:          strings.TrimSpace(ev.AgentID),
		ThreadID:    strings.TrimSpace(ev.ThreadID),
		State:       "error",
		AgentState:  "error",
		LastReport:  strings.TrimSpace(ev.Error),
		LastMessage: strings.TrimSpace(ev.Error),
	})
	sortThreads(s.state.Threads)
	sortAgents(s.state.Agents)
}

func (s *service) applyAgentRuntimeReported(ev agentdto.AgentRuntimeReported) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:      strings.TrimSpace(ev.ThreadID),
		AgentID: strings.TrimSpace(ev.AgentID),
	})
	s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
		ID:       strings.TrimSpace(ev.AgentID),
		ThreadID: strings.TrimSpace(ev.ThreadID),
		Provider: strings.TrimSpace(ev.Provider),
		Port:     ev.Port,
	})
	sortThreads(s.state.Threads)
	sortAgents(s.state.Agents)
}

func (s *service) applyThreadStarted(ev threaddto.Started) {
	s.mu.Lock()
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:           strings.TrimSpace(ev.ThreadID),
		AgentID:      strings.TrimSpace(ev.AgentID),
		State:        "running",
		ThreadStatus: "running",
	})
	sortThreads(s.state.Threads)
	patch := s.threadPatchLocked(ev.ThreadID, "thread/started")
	applyPatchStatus(&patch, "running")
	s.mu.Unlock()
	s.emitThreadPatchEvent(patch)
}

func (s *service) applyThreadStopped(ev threaddto.Stopped) {
	s.mu.Lock()
	threadID := strings.TrimSpace(ev.ThreadID)
	status := strings.TrimSpace(ev.Status)
	if status == "" {
		status = "stopped"
	}
	s.state.Threads = markThreadStopped(s.state.Threads, threadID, status)
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:           threadID,
		ThreadStatus: patchStatus(status),
	})
	sortThreads(s.state.Threads)
	patch := s.threadPatchLocked(threadID, "thread/stopped")
	applyPatchStatus(&patch, status)
	delete(s.patchSeq, threadID)
	s.mu.Unlock()
	s.emitThreadPatchEvent(patch)
}

func (s *service) applyTurnStarted(ev turndto.TurnStarted) {
	s.mu.Lock()
	turnID := strings.TrimSpace(ev.TurnID)
	if turnID != "" && s.hasRecentTurnLocked(turnID) {
		s.mu.Unlock()
		return
	}
	startedAt := cloneTime(&ev.Timestamp)
	s.state.ActiveTurn = &TurnSummary{
		ID:        turnID,
		AgentID:   strings.TrimSpace(ev.AgentID),
		ThreadID:  strings.TrimSpace(ev.ThreadID),
		Status:    "running",
		StartedAt: startedAt,
	}
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:           strings.TrimSpace(ev.ThreadID),
		AgentID:      strings.TrimSpace(ev.AgentID),
		State:        "running",
		ThreadStatus: "running",
	})
	sortThreads(s.state.Threads)
	patch := s.threadPatchLocked(ev.ThreadID, "turn/started")
	applyPatchStatus(&patch, "running")
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
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:           threadID,
		AgentID:      agentID,
		State:        "idle",
		ThreadStatus: "idle",
	})
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
	status := completionStatus(ev)
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:           strings.TrimSpace(ev.ThreadID),
		AgentID:      strings.TrimSpace(ev.AgentID),
		State:        patchStatus(status),
		ThreadStatus: patchStatus(status),
	})
	sortThreads(s.state.Threads)
	patch := s.threadPatchLocked(ev.ThreadID, "turn/completed")
	applyPatchStatus(&patch, status)
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
			return strings.TrimSpace(item.AgentState)
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
			return firstNonEmptyString(item.AgentState, item.State)
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
