package timeline

import (
	"context"
	"log/slog"
	"strings"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
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
		logger = pkglogger.Get()
	}
	return []context.CancelFunc{
		platformbus.ResilientSubscribe(dispatcher, turnStartedHandler(svc, onUpdated), logger),
		platformbus.ResilientSubscribe(dispatcher, turnCompletedHandler(svc, onUpdated), logger),
		platformbus.ResilientSubscribe(dispatcher, turnInterruptedHandler(svc, onUpdated), logger),
		// TurnInputReceived no longer projects a user timeline item; dialog
		// comes from thread/messages history RPC exclusively.
		platformbus.ResilientSubscribe(dispatcher, planDeltaHandler(svc, onUpdated), logger),
		platformbus.ResilientSubscribe(dispatcher, planUpdatedHandler(svc, onUpdated), logger),
		platformbus.ResilientSubscribe(dispatcher, agentErrorHandler(svc, onUpdated), logger),
		platformbus.ResilientSubscribe(dispatcher, agentFailedHandler(svc, onUpdated), logger),
		platformbus.ResilientSubscribe(dispatcher, itemStartedHandler(svc, onUpdated), logger),
		platformbus.ResilientSubscribe(dispatcher, itemCompletedHandler(svc, onUpdated), logger),
		platformbus.ResilientSubscribe(dispatcher, toolCallBeginHandler(svc, onUpdated), logger),
		platformbus.ResilientSubscribe(dispatcher, toolCallEndHandler(svc, onUpdated), logger),
		platformbus.ResilientSubscribe(dispatcher, approvalRequestedHandler(svc, onUpdated), logger),
		platformbus.ResilientSubscribe(dispatcher, approvalResolvedHandler(svc, onUpdated), logger),
		platformbus.ResilientSubscribe(dispatcher, reasoningDeltaHandler(svc, onUpdated), logger),
	}
}

func turnStartedHandler(svc Service, onUpdated func(string)) func(turndto.TurnStarted) {
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
		emitTimelineUpdated(onUpdated, threadID)
	}
}

func turnCompletedHandler(svc Service, onUpdated func(string)) func(turndto.TurnCompleted) {
	return func(ev turndto.TurnCompleted) {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		agentID := strings.TrimSpace(ev.AgentID)
		turnID := strings.TrimSpace(ev.TurnID)
		// Dialog (user/assistant) items are no longer projected into the uistate
		// timeline. They come exclusively from the thread/messages history RPC so
		// that live and history paths share a single source of truth and id format.
		failed := !ev.Success && shared.FirstNonEmpty(
			strings.TrimSpace(ev.Error),
			strings.TrimSpace(ev.Reason),
			strings.TrimSpace(ev.Status),
		) != ""
		status := "completed"
		if failed {
			status = "failed"
		}
		svc.Append(threadID, agentID, Item{
			ID:      timelineID("turn-end", turnID),
			Kind:    "turn_end",
			Status:  status,
			AgentID: agentID,
			TurnID:  turnID,
		})
		if failed {
			appendErrorItem(
				svc,
				threadID,
				agentID,
				turnID,
				timelineID("error", "turn", turnID),
				shared.FirstNonEmpty(
					strings.TrimSpace(ev.Error),
					strings.TrimSpace(ev.Reason),
					strings.TrimSpace(ev.Result),
					strings.TrimSpace(ev.Summary),
				),
				ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
			)
		}
		emitTimelineUpdated(onUpdated, threadID)
	}
}

func reasoningDeltaHandler(svc Service, onUpdated func(string)) func(turndto.TurnOutputDelta) {
	return func(ev turndto.TurnOutputDelta) {
		pkglogger.Get().Warn("timeline: reasoningDeltaHandler received",
			"stream", ev.Stream,
			"thread_id", ev.ThreadID,
			"turn_id", ev.TurnID,
			"delta_len", len(ev.Delta),
		)
		if !strings.EqualFold(strings.TrimSpace(ev.Stream), "reasoning") {
			return
		}
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		delta := strings.TrimSpace(ev.Delta)
		if delta == "" {
			return
		}
		agentID := strings.TrimSpace(ev.AgentID)
		turnID := strings.TrimSpace(ev.TurnID)
		id := timelineID("thinking", turnID)
		// Try to append to existing thinking item for this turn.
		if svc.UpdateByCallID(threadID, agentID, id, func(item *Item) {
			item.Text += delta
		}) {
			if onUpdated != nil {
				onUpdated(threadID)
			}
			return
		}
		svc.Append(threadID, agentID, Item{
			ID:        id,
			Kind:      "thinking",
			Text:      delta,
			Ts:        ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
			AgentID:   agentID,
			TurnID:    turnID,
			lookupKey: id,
		})
		emitTimelineUpdated(onUpdated, threadID)
	}
}

func turnInterruptedHandler(svc Service, onUpdated func(string)) func(turndto.TurnInterrupted) {
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
		emitTimelineUpdated(onUpdated, threadID)
	}
}

func itemStartedHandler(svc Service, onUpdated func(string)) func(turndto.ItemStarted) {
	return func(ev turndto.ItemStarted) {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		kind := itemKind(ev.ItemType, ev.RawType, ev.Command, ev.File)
		tool := strings.TrimSpace(ev.ToolName)
		svc.Append(threadID, strings.TrimSpace(ev.AgentID), Item{
			lookupKey: itemUpdateKey(ev.CallID),
			ID:        timelineID("item", ev.CallID, ev.ItemType, ev.Command, ev.File, ev.ToolName),
			Kind:      kind,
			Status:    "running",
			CallID:    strings.TrimSpace(ev.CallID),
			Tool:      tool,
			ToolName:  strings.TrimSpace(ev.ToolName),
			ItemType:  strings.TrimSpace(ev.ItemType),
			Command:   strings.TrimSpace(ev.Command),
			File:      strings.TrimSpace(ev.File),
			AgentID:   strings.TrimSpace(ev.AgentID),
			TurnID:    strings.TrimSpace(ev.TurnID),
			Ts:        ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
		})
		emitTimelineUpdated(onUpdated, threadID)
	}
}

func toolCallBeginHandler(svc Service, onUpdated func(string)) func(tooldto.ToolCallBegin) {
	return func(ev tooldto.ToolCallBegin) {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		tool := strings.TrimSpace(ev.ToolName)
		svc.Append(threadID, strings.TrimSpace(ev.AgentID), Item{
			lookupKey: toolUpdateKey(ev.CallID, tool),
			ID:        timelineID("tool", ev.CallID, ev.ToolName),
			Kind:      "tool",
			Status:    "running",
			CallID:    strings.TrimSpace(ev.CallID),
			RequestID: ev.RequestID,
			Tool:      tool,
			ToolName:  tool,
			AgentID:   strings.TrimSpace(ev.AgentID),
			TurnID:    strings.TrimSpace(ev.TurnID),
			Ts:        ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
		})
		emitTimelineUpdated(onUpdated, threadID)
	}
}

func toolCallEndHandler(svc Service, onUpdated func(string)) func(tooldto.ToolCallEnd) {
	return func(ev tooldto.ToolCallEnd) {
		threadID := strings.TrimSpace(ev.ThreadID)
		updateKey := toolUpdateKey(ev.CallID, ev.ToolName)
		if threadID == "" || updateKey == "" {
			return
		}
		success := ev.Success
		updated := svc.UpdateByCallID(threadID, strings.TrimSpace(ev.AgentID), updateKey, func(it *Item) {
			applyToolCallCompleted(it, ev, success)
		})
		if updated {
			emitTimelineUpdated(onUpdated, threadID)
			return
		}
		if appendCompletedToolFallback(svc, threadID, ev, updateKey, success) {
			emitTimelineUpdated(onUpdated, threadID)
		}
	}
}

func approvalRequestedHandler(svc Service, onUpdated func(string)) func(tooldto.ToolApprovalRequested) {
	return func(ev tooldto.ToolApprovalRequested) {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		tool := strings.TrimSpace(ev.ToolName)
		svc.Append(threadID, strings.TrimSpace(ev.AgentID), Item{
			lookupKey: approvalUpdateKey(ev.ApprovalID, ev.CallID),
			ID:        timelineID("approval", ev.ApprovalID, ev.CallID, ev.ToolName, ev.Kind),
			Kind:      "approval",
			Status:    "pending",
			CallID:    strings.TrimSpace(ev.CallID),
			RequestID: ev.RequestID,
			Tool:      tool,
			ToolName:  tool,
			ItemType:  strings.TrimSpace(ev.Kind),
			AgentID:   strings.TrimSpace(ev.AgentID),
			TurnID:    strings.TrimSpace(ev.TurnID),
			Ts:        ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
		})
		emitTimelineUpdated(onUpdated, threadID)
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
			it.Kind = "approval"
			it.Status = status
			it.Done = true
			if strings.TrimSpace(it.Ts) == "" {
				it.Ts = ev.Timestamp.Format("2006-01-02T15:04:05Z07:00")
			}
			it.Tool = shared.FirstNonEmpty(strings.TrimSpace(ev.ToolName), it.Tool)
			it.ToolName = shared.FirstNonEmpty(strings.TrimSpace(ev.ToolName), it.ToolName)
		})
		if updated {
			if onUpdated != nil {
				onUpdated(threadID)
			}
			return
		}
		svc.Append(threadID, strings.TrimSpace(ev.AgentID), Item{
			lookupKey: updateKey,
			ID:        timelineID("approval", ev.ApprovalID, ev.CallID, ev.ToolName, ev.Kind),
			Kind:      "approval",
			Status:    status,
			CallID:    strings.TrimSpace(ev.CallID),
			Tool:      strings.TrimSpace(ev.ToolName),
			ToolName:  strings.TrimSpace(ev.ToolName),
			ItemType:  strings.TrimSpace(ev.Kind),
			Done:      true,
			AgentID:   strings.TrimSpace(ev.AgentID),
			TurnID:    strings.TrimSpace(ev.TurnID),
			Ts:        ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
		})
		emitTimelineUpdated(onUpdated, threadID)
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

func toolUpdateKey(callID, tool string) string {
	return timelineID("tool", tool, callID)
}

func approvalUpdateKey(approvalID, callID string) string {
	return timelineID("approval", shared.FirstNonEmpty(approvalID, callID))
}
