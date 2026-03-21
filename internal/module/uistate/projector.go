package uistate

import (
	"context"
	"strings"

	"github.com/kelindar/event"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	workspacedto "github.com/anthropic-ai/super-agent-v3/internal/dto/workspace"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
)

func registerProjectionSubscriptions(dispatcher *event.Dispatcher, svc *service) []context.CancelFunc {
	return []context.CancelFunc{
		platformbus.ResilientSubscribe(dispatcher, svc.applyAgentStateChanged, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyAgentLaunched, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyAgentStopped, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyAgentRecovering, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyAgentFailed, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyAgentRuntimeReported, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyThreadStarted, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyThreadStopped, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyTurnStarted, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyTurnInterrupted, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyTurnCompleted, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyTurnOutputDelta, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyWorkspaceRunCreated, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyWorkspaceRunStatusChanged, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyWorkspaceRunMerged, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyWorkspaceRunAborted, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyWorkspaceRunMergeError, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyTokensUpdated, svc.logger),
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
	s.state.TokenUsage = TokenUsage{
		InputTokens:         ev.InputTokens,
		OutputTokens:        ev.OutputTokens,
		TotalTokens:         ev.TotalTokens,
		ContextWindowTokens: ev.ContextWindowTokens,
	}
	patch := s.threadPatchLocked(ev.ThreadID, "thread/tokenusage/updated")
	patch.TokenUsage = tokenUsagePatch(ev)
	s.mu.Unlock()
	s.emitThreadPatchEvent(patch)
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

func appendLastMessageLocked(items *[]ThreadSummary, threadID, agentID, delta string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || items == nil {
		return
	}
	nextMessage := strings.TrimSpace(delta)
	for i := range *items {
		if strings.TrimSpace((*items)[i].ID) != threadID {
			continue
		}
		(*items)[i].AgentID = chooseString(strings.TrimSpace(agentID), (*items)[i].AgentID)
		(*items)[i].LastMessage = mergeLastMessage((*items)[i].LastMessage, nextMessage)
		return
	}
	*items = append(*items, ThreadSummary{
		ID:          threadID,
		AgentID:     strings.TrimSpace(agentID),
		LastMessage: nextMessage,
	})
}

func appendAgentMessageLocked(items *[]AgentSummary, agentID, threadID, delta string) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || items == nil {
		return
	}
	nextMessage := strings.TrimSpace(delta)
	for i := range *items {
		if strings.TrimSpace((*items)[i].ID) != agentID {
			continue
		}
		(*items)[i].ThreadID = chooseString(strings.TrimSpace(threadID), (*items)[i].ThreadID)
		(*items)[i].LastMessage = mergeLastMessage((*items)[i].LastMessage, nextMessage)
		return
	}
	*items = append(*items, AgentSummary{
		ID:          agentID,
		ThreadID:    strings.TrimSpace(threadID),
		LastMessage: nextMessage,
	})
}

func mergeLastMessage(current, delta string) string {
	current = strings.TrimSpace(current)
	delta = strings.TrimSpace(delta)
	if current == "" {
		return clipLastMessage(delta)
	}
	if delta == "" {
		return clipLastMessage(current)
	}
	return clipLastMessage(current + delta)
}

func clipLastMessage(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 240 {
		return value
	}
	return value[len(value)-240:]
}
