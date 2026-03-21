package uistate

import (
	"context"
	"strings"

	"github.com/kelindar/event"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	workspacedto "github.com/anthropic-ai/super-agent-v3/internal/dto/workspace"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
)

func registerProjectionSubscriptions(dispatcher *event.Dispatcher, svc *service) []context.CancelFunc {
	return []context.CancelFunc{
		platformbus.ResilientSubscribe(dispatcher, svc.applyAgentStateChanged, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyThreadStarted, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyThreadStopped, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyTurnStarted, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyTurnCompleted, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyWorkspaceRunCreated, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyWorkspaceRunStatusChanged, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyWorkspaceRunMerged, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyWorkspaceRunAborted, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyWorkspaceRunMergeError, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyTokensUpdated, svc.logger),
	}
}

func (s *service) applyAgentStateChanged(ev agentdto.StateChanged) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:      strings.TrimSpace(ev.ThreadID),
		AgentID: strings.TrimSpace(ev.AgentID),
	})
	s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
		ID:       strings.TrimSpace(ev.AgentID),
		ThreadID: strings.TrimSpace(ev.ThreadID),
		State:    strings.TrimSpace(ev.NewState),
	})
	sortThreads(s.state.Threads)
	sortAgents(s.state.Agents)
}

func (s *service) applyThreadStarted(ev threaddto.Started) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:      strings.TrimSpace(ev.ThreadID),
		AgentID: strings.TrimSpace(ev.AgentID),
		State:   "running",
	})
	sortThreads(s.state.Threads)
}

func (s *service) applyThreadStopped(ev threaddto.Stopped) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := strings.TrimSpace(ev.Status)
	if status == "" {
		status = "stopped"
	}
	s.state.Threads = markThreadStopped(s.state.Threads, ev.ThreadID, status)
	sortThreads(s.state.Threads)
}

func (s *service) applyTurnStarted(ev turndto.TurnStarted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	startedAt := cloneTime(&ev.Timestamp)
	s.state.ActiveTurn = &TurnSummary{
		ID:        strings.TrimSpace(ev.TurnID),
		AgentID:   strings.TrimSpace(ev.AgentID),
		ThreadID:  strings.TrimSpace(ev.ThreadID),
		Status:    "running",
		StartedAt: startedAt,
	}
}

func (s *service) applyTurnCompleted(ev turndto.TurnCompleted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	completed := completedTurnSummary(s.state.ActiveTurn, ev)
	s.state.RecentTurns = pushRecentTurn(s.state.RecentTurns, completed, recentTurnLimit)
	if s.state.ActiveTurn != nil && s.state.ActiveTurn.ID == completed.ID {
		s.state.ActiveTurn = nil
	}
}

func (s *service) applyWorkspaceRunCreated(ev workspacedto.WorkspaceRunCreated) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workspaceByKey[ev.RunKey] = mergeWorkspaceRun(s.workspaceByKey[ev.RunKey], WorkspaceRunSummary{
		RunKey:        strings.TrimSpace(ev.RunKey),
		DagKey:        strings.TrimSpace(ev.DagKey),
		Status:        "created",
		SourceRoot:    strings.TrimSpace(ev.SourceRoot),
		WorkspacePath: strings.TrimSpace(ev.WorkspacePath),
		CreatedBy:     strings.TrimSpace(ev.CreatedBy),
		UpdatedAt:     cloneTime(&ev.Timestamp),
	})
}

func (s *service) applyWorkspaceRunStatusChanged(ev workspacedto.WorkspaceRunStatusChanged) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workspaceByKey[ev.RunKey] = mergeWorkspaceRun(s.workspaceByKey[ev.RunKey], WorkspaceRunSummary{
		RunKey:    strings.TrimSpace(ev.RunKey),
		DagKey:    strings.TrimSpace(ev.DagKey),
		Status:    strings.TrimSpace(ev.NewStatus),
		UpdatedBy: strings.TrimSpace(ev.UpdatedBy),
		UpdatedAt: cloneTime(&ev.Timestamp),
	})
}

func (s *service) applyWorkspaceRunMerged(ev workspacedto.WorkspaceRunMerged) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workspaceByKey[ev.RunKey] = mergeWorkspaceRun(s.workspaceByKey[ev.RunKey], WorkspaceRunSummary{
		RunKey:          strings.TrimSpace(ev.RunKey),
		DagKey:          strings.TrimSpace(ev.DagKey),
		Status:          "merged",
		SourceRoot:      strings.TrimSpace(ev.SourceRoot),
		WorkspacePath:   strings.TrimSpace(ev.WorkspacePath),
		UpdatedBy:       strings.TrimSpace(ev.UpdatedBy),
		MergedFileCount: ev.MergedFileCount,
		UpdatedAt:       cloneTime(&ev.Timestamp),
	})
}

func (s *service) applyWorkspaceRunAborted(ev workspacedto.WorkspaceRunAborted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workspaceByKey[ev.RunKey] = mergeWorkspaceRun(s.workspaceByKey[ev.RunKey], WorkspaceRunSummary{
		RunKey:    strings.TrimSpace(ev.RunKey),
		DagKey:    strings.TrimSpace(ev.DagKey),
		Status:    "aborted",
		UpdatedBy: strings.TrimSpace(ev.UpdatedBy),
		Message:   strings.TrimSpace(ev.Reason),
		UpdatedAt: cloneTime(&ev.Timestamp),
	})
}

func (s *service) applyWorkspaceRunMergeError(ev workspacedto.WorkspaceRunMergeError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workspaceByKey[ev.RunKey] = mergeWorkspaceRun(s.workspaceByKey[ev.RunKey], WorkspaceRunSummary{
		RunKey:        strings.TrimSpace(ev.RunKey),
		DagKey:        strings.TrimSpace(ev.DagKey),
		Status:        "merge_error",
		SourceRoot:    strings.TrimSpace(ev.SourceRoot),
		WorkspacePath: strings.TrimSpace(ev.WorkspacePath),
		UpdatedBy:     strings.TrimSpace(ev.UpdatedBy),
		Conflicts:     ev.Conflicts,
		Errors:        ev.Errors,
		Message:       strings.TrimSpace(ev.Message),
		UpdatedAt:     cloneTime(&ev.Timestamp),
	})
}

func (s *service) applyTokensUpdated(ev uidto.UITokensUpdated) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.TokenUsage = TokenUsage{
		InputTokens:         ev.InputTokens,
		OutputTokens:        ev.OutputTokens,
		TotalTokens:         ev.TotalTokens,
		ContextWindowTokens: ev.ContextWindowTokens,
	}
}

func completedTurnSummary(current *TurnSummary, ev turndto.TurnCompleted) TurnSummary {
	summary := TurnSummary{
		ID:          strings.TrimSpace(ev.TurnID),
		AgentID:     strings.TrimSpace(ev.AgentID),
		ThreadID:    strings.TrimSpace(ev.ThreadID),
		Status:      completionStatus(ev),
		Error:       strings.TrimSpace(ev.Error),
		Reason:      strings.TrimSpace(ev.Reason),
		CompletedAt: cloneTime(&ev.Timestamp),
	}
	if current != nil && current.ID == summary.ID {
		summary = *cloneTurn(current)
		summary.Status = completionStatus(ev)
		summary.Error = strings.TrimSpace(ev.Error)
		summary.Reason = strings.TrimSpace(ev.Reason)
		summary.CompletedAt = cloneTime(&ev.Timestamp)
	}
	success := ev.Success
	summary.Success = &success
	if summary.ID == "" {
		summary.ID = strings.TrimSpace(ev.TurnID)
	}
	if summary.AgentID == "" {
		summary.AgentID = strings.TrimSpace(ev.AgentID)
	}
	if summary.ThreadID == "" {
		summary.ThreadID = strings.TrimSpace(ev.ThreadID)
	}
	return summary
}

func completionStatus(ev turndto.TurnCompleted) string {
	if status := strings.TrimSpace(ev.Status); status != "" {
		return status
	}
	if ev.Success {
		return "completed"
	}
	return "failed"
}

func mergeWorkspaceRun(current, next WorkspaceRunSummary) WorkspaceRunSummary {
	current.RunKey = chooseString(next.RunKey, current.RunKey)
	current.DagKey = chooseString(next.DagKey, current.DagKey)
	current.Status = chooseString(next.Status, current.Status)
	current.SourceRoot = chooseString(next.SourceRoot, current.SourceRoot)
	current.WorkspacePath = chooseString(next.WorkspacePath, current.WorkspacePath)
	current.CreatedBy = chooseString(next.CreatedBy, current.CreatedBy)
	current.UpdatedBy = chooseString(next.UpdatedBy, current.UpdatedBy)
	current.MergedFileCount = choosePositiveInt(next.MergedFileCount, current.MergedFileCount)
	current.Conflicts = choosePositiveInt(next.Conflicts, current.Conflicts)
	current.Errors = choosePositiveInt(next.Errors, current.Errors)
	current.Message = chooseString(next.Message, current.Message)
	if next.UpdatedAt != nil {
		current.UpdatedAt = cloneTime(next.UpdatedAt)
	}
	return current
}

func chooseString(next, current string) string {
	if next != "" {
		return next
	}
	return current
}

func choosePositiveInt(next, current int) int {
	if next > 0 {
		return next
	}
	return current
}
