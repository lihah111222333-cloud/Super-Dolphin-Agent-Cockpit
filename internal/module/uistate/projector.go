package uistate

import (
	"context"
	"strings"

	"github.com/kelindar/event"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate/timeline"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

func registerProjectionSubscriptions(dispatcher *event.Dispatcher, svc *service) []context.CancelFunc {
	cancels := []context.CancelFunc{
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
		platformbus.ResilientSubscribe(dispatcher, svc.applyTurnResumed, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyTurnInputReceived, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyTurnOutputDelta, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyItemStarted, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyItemCompleted, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyToolCallBegin, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyToolCallEnd, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyToolApprovalRequested, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyToolApprovalResolved, svc.logger),
		platformbus.ResilientSubscribe(dispatcher, svc.applyTokensUpdated, svc.logger),
	}
	onTimelineUpdated := func(threadID string) {
		svc.mu.Lock()
		ev := svc.projectionUpdatedLocked("timeline")
		svc.mu.Unlock()
		ev.ThreadID = threadID
		svc.emitProjectionUpdatedEvents(ev)
	}
	if svc.timeline != nil {
		cancels = append(cancels, timeline.RegisterSubscriptions(dispatcher, svc.timeline, svc.logger, onTimelineUpdated)...)
	}
	return cancels
}

func (s *service) applyTokensUpdated(ev uidto.UITokensUpdated) {
	threadID := strings.TrimSpace(ev.ThreadID)
	applyMutation(s, threadID, func() {
		s.state.TokenUsage = TokenUsage{
			InputTokens:         ev.InputTokens,
			OutputTokens:        ev.OutputTokens,
			TotalTokens:         ev.TotalTokens,
			ContextWindowTokens: ev.ContextWindowTokens,
		}
	}, func() uidto.UIThreadPatch {
		patch := s.threadPatchLocked(threadID, "thread/tokenusage/updated")
		patch.TokenUsage = tokenUsagePatch(ev)
		return patch
	})
}
func (s *service) applyItemStarted(ev turndto.ItemStarted) {
	activity := classifyItemActivity(ev.ItemType, ev.RawType, ev.Command, ev.File)
	if activity == "" {
		return
	}
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	ok := false
	applyMutation(s, threadID, func() {
		var rt *threadActivity
		threadID, rt, ok = s.eventThreadActivityLocked(threadID, agentID, "item/started")
		if !ok {
			return
		}
		if rt.turnDepth == 0 {
			rt.turnDepth = 1
		}
		switch activity {
		case "editing":
			rt.editDepth++
		case "command":
			rt.commandDepth++
		}
	}, func() uidto.UIThreadPatch {
		if !ok {
			return uidto.UIThreadPatch{}
		}
		return s.refreshThreadPatchLocked(threadID, agentID, "item/started")
	})
}
func (s *service) applyItemCompleted(ev turndto.ItemCompleted) {
	activity := classifyItemActivity(ev.ItemType, ev.RawType, ev.Command, ev.File)
	if activity == "" {
		return
	}
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	ok := false
	applyMutation(s, threadID, func() {
		var rt *threadActivity
		threadID, rt, ok = s.eventThreadActivityLocked(threadID, agentID, "item/completed")
		if !ok {
			return
		}
		switch activity {
		case "editing":
			rt.editDepth = adjustDepth(rt.editDepth, -1)
		case "command":
			rt.commandDepth = adjustDepth(rt.commandDepth, -1)
		}
	}, func() uidto.UIThreadPatch {
		if !ok {
			return uidto.UIThreadPatch{}
		}
		return s.refreshThreadPatchLocked(threadID, agentID, "item/completed")
	})
}
func (s *service) applyToolCallBegin(ev tooldto.ToolCallBegin) {
	activity := classifyToolActivity(ev.ToolName)
	if activity == "" {
		return
	}
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	ok := false
	applyMutation(s, threadID, func() {
		var rt *threadActivity
		threadID, rt, ok = s.eventThreadActivityLocked(threadID, agentID, "tool/call")
		if !ok {
			return
		}
		if rt.turnDepth == 0 {
			rt.turnDepth = 1
		}
		if activity == "collab" {
			rt.collabDepth++
		} else {
			rt.toolDepth++
		}
	}, func() uidto.UIThreadPatch {
		if !ok {
			return uidto.UIThreadPatch{}
		}
		return s.refreshThreadPatchLocked(threadID, agentID, "tool/call")
	})
}
func (s *service) applyToolCallEnd(ev tooldto.ToolCallEnd) {
	activity := classifyToolActivity(ev.ToolName)
	if activity == "" {
		return
	}
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	ok := false
	applyMutation(s, threadID, func() {
		var rt *threadActivity
		threadID, rt, ok = s.eventThreadActivityLocked(threadID, agentID, "tool/completed")
		if !ok {
			return
		}
		if activity == "collab" {
			rt.collabDepth = adjustDepth(rt.collabDepth, -1)
		} else {
			rt.toolDepth = adjustDepth(rt.toolDepth, -1)
		}
	}, func() uidto.UIThreadPatch {
		if !ok {
			return uidto.UIThreadPatch{}
		}
		return s.refreshThreadPatchLocked(threadID, agentID, "tool/completed")
	})
}
func (s *service) applyToolApprovalRequested(ev tooldto.ToolApprovalRequested) {
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	waitsForInput := strings.EqualFold(strings.TrimSpace(ev.Kind), "request_user_input")
	ok := false
	applyMutation(s, threadID, func() {
		var rt *threadActivity
		threadID, rt, ok = s.eventThreadActivityLocked(threadID, agentID, "tool/approvalRequested")
		if !ok {
			return
		}
		if rt.turnDepth == 0 {
			rt.turnDepth = 1
		}
		rt.approvalDepth++
		if waitsForInput {
			rt.inputApprovalDepth++
			s.setThreadOverlayLocked(threadID, overlayTypeTerminalWait, "等待终端输入", overlayPriorityTerminalWait, 0)
		}
	}, func() uidto.UIThreadPatch {
		if !ok {
			return uidto.UIThreadPatch{}
		}
		return s.refreshThreadPatchLocked(threadID, agentID, "tool/approvalRequested")
	})
}
func (s *service) applyToolApprovalResolved(ev tooldto.ToolApprovalResolved) {
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	waitsForInput := strings.EqualFold(strings.TrimSpace(ev.Kind), "request_user_input")
	ok := false
	applyMutation(s, threadID, func() {
		var rt *threadActivity
		threadID, rt, ok = s.eventThreadActivityLocked(threadID, agentID, "tool/approvalResolved")
		if !ok {
			return
		}
		rt.approvalDepth = adjustDepth(rt.approvalDepth, -1)
		if waitsForInput {
			rt.inputApprovalDepth = adjustDepth(rt.inputApprovalDepth, -1)
			if rt.inputApprovalDepth == 0 {
				s.clearThreadOverlayLocked(threadID, overlayTypeTerminalWait)
			}
		}
	}, func() uidto.UIThreadPatch {
		if !ok {
			return uidto.UIThreadPatch{}
		}
		return s.refreshThreadPatchLocked(threadID, agentID, "tool/approvalResolved")
	})
}

func completedTurnSummary(current *TurnSummary, ev turndto.TurnCompleted) TurnSummary {
	summary := TurnSummary{
		ID:          strings.TrimSpace(ev.TurnID),
		AgentID:     strings.TrimSpace(ev.AgentID),
		ThreadID:    strings.TrimSpace(ev.ThreadID),
		Status:      completionStatus(ev),
		Error:       strings.TrimSpace(ev.Error),
		Reason:      strings.TrimSpace(ev.Reason),
		CompletedAt: shared.CloneTime(&ev.Timestamp),
	}
	if current != nil && current.ID == summary.ID {
		summary = *cloneTurn(current)
		summary.Status = completionStatus(ev)
		summary.Error = strings.TrimSpace(ev.Error)
		summary.Reason = strings.TrimSpace(ev.Reason)
		summary.CompletedAt = shared.CloneTime(&ev.Timestamp)
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
