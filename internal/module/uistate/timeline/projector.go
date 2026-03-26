package timeline

import (
	"context"
	"log/slog"
	"strings"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/kelindar/event"
)

func RegisterSubscriptions(
	dispatcher *event.Dispatcher,
	svc Service,
	logger *slog.Logger,
	onUpdated func(threadID string),
) []context.CancelFunc {
	if dispatcher == nil || svc == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return []context.CancelFunc{
		platformbus.ResilientSubscribe(dispatcher, turnStartedHandler(svc), logger),
		platformbus.ResilientSubscribe(dispatcher, turnCompletedHandler(svc), logger),
		platformbus.ResilientSubscribe(dispatcher, turnInterruptedHandler(svc), logger),
		platformbus.ResilientSubscribe(dispatcher, itemStartedHandler(svc), logger),
		platformbus.ResilientSubscribe(dispatcher, itemCompletedHandler(svc, onUpdated), logger),
		platformbus.ResilientSubscribe(dispatcher, toolCallBeginHandler(svc), logger),
		platformbus.ResilientSubscribe(dispatcher, toolCallEndHandler(svc, onUpdated), logger),
		platformbus.ResilientSubscribe(dispatcher, approvalRequestedHandler(svc), logger),
		platformbus.ResilientSubscribe(dispatcher, approvalResolvedHandler(svc, onUpdated), logger),
	}
}

func turnStartedHandler(svc Service) func(turndto.TurnStarted) {
	return func(ev turndto.TurnStarted) {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		svc.Append(threadID, strings.TrimSpace(ev.AgentID), Item{
			ID:      timelineID("turn", ev.TurnID),
			Kind:    "turn_start",
			Status:  "running",
			AgentID: strings.TrimSpace(ev.AgentID),
			TurnID:  strings.TrimSpace(ev.TurnID),
		})
	}
}

func turnCompletedHandler(svc Service) func(turndto.TurnCompleted) {
	return func(ev turndto.TurnCompleted) {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		svc.Append(threadID, strings.TrimSpace(ev.AgentID), Item{
			ID:      timelineID("turn-end", ev.TurnID),
			Kind:    "turn_end",
			Status:  "completed",
			AgentID: strings.TrimSpace(ev.AgentID),
			TurnID:  strings.TrimSpace(ev.TurnID),
		})
	}
}

func turnInterruptedHandler(svc Service) func(turndto.TurnInterrupted) {
	return func(ev turndto.TurnInterrupted) {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		svc.Append(threadID, strings.TrimSpace(ev.AgentID), Item{
			ID:      timelineID("turn-int", ev.TurnID),
			Kind:    "turn_interrupted",
			Status:  "interrupted",
			AgentID: strings.TrimSpace(ev.AgentID),
			TurnID:  strings.TrimSpace(ev.TurnID),
		})
	}
}

func itemStartedHandler(svc Service) func(turndto.ItemStarted) {
	return func(ev turndto.ItemStarted) {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		svc.Append(threadID, strings.TrimSpace(ev.AgentID), Item{
			lookupKey: itemUpdateKey(ev.CallID),
			ID:        timelineID("item", ev.CallID, ev.ItemType, ev.Command, ev.File, ev.ToolName),
			Kind:      "item",
			Status:    "running",
			CallID:    strings.TrimSpace(ev.CallID),
			ToolName:  strings.TrimSpace(ev.ToolName),
			ItemType:  strings.TrimSpace(ev.ItemType),
			Command:   strings.TrimSpace(ev.Command),
			File:      strings.TrimSpace(ev.File),
			AgentID:   strings.TrimSpace(ev.AgentID),
			TurnID:    strings.TrimSpace(ev.TurnID),
		})
	}
}

func itemCompletedHandler(svc Service, onUpdated func(string)) func(turndto.ItemCompleted) {
	return func(ev turndto.ItemCompleted) {
		threadID := strings.TrimSpace(ev.ThreadID)
		updateKey := itemUpdateKey(ev.CallID)
		if threadID == "" || updateKey == "" {
			return
		}
		success := ev.Success
		updated := svc.UpdateByCallID(threadID, strings.TrimSpace(ev.AgentID), updateKey, func(it *Item) {
			it.Status = "completed"
			it.Success = &success
			if errText := strings.TrimSpace(ev.Error); errText != "" {
				it.Error = errText
			}
		})
		if updated && onUpdated != nil {
			onUpdated(threadID)
		}
	}
}

func toolCallBeginHandler(svc Service) func(tooldto.ToolCallBegin) {
	return func(ev tooldto.ToolCallBegin) {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		svc.Append(threadID, strings.TrimSpace(ev.AgentID), Item{
			lookupKey: toolUpdateKey(ev.CallID),
			ID:        timelineID("tool", ev.CallID, ev.ToolName),
			Kind:      "tool_call",
			Status:    "running",
			CallID:    strings.TrimSpace(ev.CallID),
			RequestID: ev.RequestID,
			ToolName:  strings.TrimSpace(ev.ToolName),
			AgentID:   strings.TrimSpace(ev.AgentID),
			TurnID:    strings.TrimSpace(ev.TurnID),
		})
	}
}

func toolCallEndHandler(svc Service, onUpdated func(string)) func(tooldto.ToolCallEnd) {
	return func(ev tooldto.ToolCallEnd) {
		threadID := strings.TrimSpace(ev.ThreadID)
		updateKey := toolUpdateKey(ev.CallID)
		if threadID == "" || updateKey == "" {
			return
		}
		success := ev.Success
		updated := svc.UpdateByCallID(threadID, strings.TrimSpace(ev.AgentID), updateKey, func(it *Item) {
			it.Status = "completed"
			it.Success = &success
			if errText := strings.TrimSpace(ev.Error); errText != "" {
				it.Error = errText
			}
		})
		if updated && onUpdated != nil {
			onUpdated(threadID)
		}
	}
}

func approvalRequestedHandler(svc Service) func(tooldto.ToolApprovalRequested) {
	return func(ev tooldto.ToolApprovalRequested) {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		svc.Append(threadID, strings.TrimSpace(ev.AgentID), Item{
			lookupKey: approvalUpdateKey(ev.ApprovalID, ev.CallID),
			ID:        timelineID("approval", ev.ApprovalID, ev.CallID, ev.ToolName, ev.Kind),
			Kind:      "approval_request",
			Status:    "pending",
			CallID:    strings.TrimSpace(ev.CallID),
			RequestID: ev.RequestID,
			ToolName:  strings.TrimSpace(ev.ToolName),
			ItemType:  strings.TrimSpace(ev.Kind),
			AgentID:   strings.TrimSpace(ev.AgentID),
			TurnID:    strings.TrimSpace(ev.TurnID),
		})
	}
}

func approvalResolvedHandler(svc Service, onUpdated func(string)) func(tooldto.ToolApprovalResolved) {
	return func(ev tooldto.ToolApprovalResolved) {
		threadID := strings.TrimSpace(ev.ThreadID)
		updateKey := approvalUpdateKey(ev.ApprovalID, ev.CallID)
		if threadID == "" || updateKey == "" {
			return
		}
		status := "rejected"
		if ev.Approved {
			status = "approved"
		}
		updated := svc.UpdateByCallID(threadID, strings.TrimSpace(ev.AgentID), updateKey, func(it *Item) {
			it.Status = status
		})
		if updated && onUpdated != nil {
			onUpdated(threadID)
		}
	}
}

func timelineID(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	return strings.Join(cleaned, ":")
}

func itemUpdateKey(callID string) string {
	return timelineID("item", callID)
}

func toolUpdateKey(callID string) string {
	return timelineID("tool", callID)
}

func approvalUpdateKey(approvalID, callID string) string {
	return timelineID("approval", firstNonEmpty(approvalID, callID))
}

func firstNonEmpty(parts ...string) string {
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
