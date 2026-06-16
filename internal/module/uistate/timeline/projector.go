package timeline

import (
	"context"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

var timelineOutputDeltaLogSampler = pkglogger.NewEverySampler(1000)

// RegisterSubscriptions 注册subscriptions。
func RegisterSubscriptions(
	dispatcher *event.Dispatcher,
	svc Service,
	logger *pkglogger.Logger,
	onUpdated func(threadID string),
) []context.CancelFunc {
	if dispatcher == nil || svc == nil {
		return nil
	}
	if logger == nil {
		logger = pkglogger.Get()
	}
	return []context.CancelFunc{
		contract.ResilientSubscribe(dispatcher, turnStartedHandler(svc, onUpdated), logger),
		contract.ResilientSubscribe(dispatcher, turnCompletedHandler(svc, onUpdated), logger),
		contract.ResilientSubscribe(dispatcher, turnInterruptedHandler(svc, onUpdated), logger),
		// TurnInputReceived no longer projects a user timeline item; dialog
		// comes from thread/messages history RPC exclusively.
		contract.ResilientSubscribe(dispatcher, planDeltaHandler(svc, onUpdated), logger),
		contract.ResilientSubscribe(dispatcher, planUpdatedHandler(svc, onUpdated), logger),
		contract.ResilientSubscribe(dispatcher, agentErrorHandler(svc, onUpdated), logger),
		contract.ResilientSubscribe(dispatcher, agentFailedHandler(svc, onUpdated), logger),
		contract.ResilientSubscribe(dispatcher, itemStartedHandler(svc, onUpdated), logger),
		contract.ResilientSubscribe(dispatcher, itemCompletedHandler(svc, onUpdated), logger),
		contract.ResilientSubscribe(dispatcher, toolCallBeginHandler(svc, onUpdated), logger),
		contract.ResilientSubscribe(dispatcher, toolCallEndHandler(svc, onUpdated), logger),
		contract.ResilientSubscribe(dispatcher, approvalRequestedHandler(svc, onUpdated), logger),
		contract.ResilientSubscribe(dispatcher, approvalResolvedHandler(svc, onUpdated), logger),
		contract.ResilientSubscribe(dispatcher, reasoningDeltaHandler(svc, onUpdated), logger),
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

// turnCompletedHandler 处理turncompleted处理器。
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
		failed := !ev.Success && util.FirstNonEmpty(
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
				util.FirstNonEmpty(
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

// reasoningDeltaHandler 处理reasoningdelta处理器。
func reasoningDeltaHandler(svc Service, onUpdated func(string)) func(turndto.TurnOutputDelta) {
	return func(ev turndto.TurnOutputDelta) {
		if timelineOutputDeltaLogSampler.ShouldLog(ev.Stream) {
			pkglogger.Get().Debug("timeline: reasoningDeltaHandler received",
				"sample_rate", "0.1%",
				"stream", ev.Stream,
				"thread_id", ev.ThreadID,
				"turn_id", ev.TurnID,
				"delta_len", len(ev.Delta),
			)
		}
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
			Preview:   previewText(ev.ArgumentsPreview),
			AgentID:   strings.TrimSpace(ev.AgentID),
			TurnID:    strings.TrimSpace(ev.TurnID),
			Ts:        ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
		})
		emitTimelineUpdated(onUpdated, threadID)
	}
}

// toolCallEndHandler 处理工具callend处理器。
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
		if recoverToolEndByCallID(svc, threadID, ev, success) {
			emitTimelineUpdated(onUpdated, threadID)
			return
		}
		if appendCompletedToolFallback(svc, threadID, ev, updateKey, success) {
			emitTimelineUpdated(onUpdated, threadID)
		}
	}
}

// recoverToolEndByCallID handles ToolName-less End events: some runtimes
// only echo CallID on the End event. We scan the timeline backwards for
// a matching tool item and complete it.
// recoverToolEndByCallID 按callID恢复工具end。
func recoverToolEndByCallID(svc Service, threadID string, ev tooldto.ToolCallEnd, success bool) bool {
	if strings.TrimSpace(ev.ToolName) != "" {
		return false
	}
	callID := strings.TrimSpace(ev.CallID)
	if callID == "" {
		return false
	}
	items := svc.GetByThread(threadID)
	for i := len(items) - 1; i >= 0; i-- {
		it := items[i]
		if it.Kind != "tool" || strings.TrimSpace(it.CallID) != callID {
			continue
		}
		recoveredKey := toolUpdateKey(callID, it.ToolName)
		if recoveredKey == "" {
			return false
		}
		return svc.UpdateByCallID(threadID, strings.TrimSpace(ev.AgentID), recoveredKey, func(target *Item) {
			applyToolCallCompleted(target, ev, success)
		})
	}
	return false
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

// approvalResolvedHandler 处理审批已解析处理器。
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
			it.Tool = util.FirstNonEmpty(strings.TrimSpace(ev.ToolName), it.Tool)
			it.ToolName = util.FirstNonEmpty(strings.TrimSpace(ev.ToolName), it.ToolName)
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
	return timelineID("approval", util.FirstNonEmpty(approvalID, callID))
}
