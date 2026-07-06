package timeline

import (
	"context"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"log/slog"
	"strings"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate/terminalstatus"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

var timelineOutputDeltaLogSampler = pkglogger.NewEverySampler(1000)

// RegisterSubscriptions 注册 timeline 投影订阅，并把每个 cancel 返回给上层生命周期管理。
// 对话消息不在这里投影，避免和 thread/messages 历史接口生成重复消息行。
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
		contract.ResilientSubscribe(dispatcher, turnStartedHandler(svc, onUpdated), logger),
		contract.ResilientSubscribe(dispatcher, turnCompletedHandler(svc, onUpdated), logger),
		contract.ResilientSubscribe(dispatcher, turnInterruptedHandler(svc, onUpdated), logger),
		// 用户输入只由 thread/messages 历史接口提供，timeline 不再重复追加对话行。
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

// turnCompletedHandler 追加 turn 结束标记，并在失败时补一条 error item。
// 对话消息不再由 timeline 投影，避免 live 与历史消息 RPC 产生两套 ID。
func turnCompletedHandler(svc Service, onUpdated func(string)) func(turndto.TurnCompleted) {
	return func(ev turndto.TurnCompleted) {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		agentID := strings.TrimSpace(ev.AgentID)
		turnID := strings.TrimSpace(ev.TurnID)
		status := terminalstatus.Status(ev.Success, ev.Status, ev.Reason, ev.Error)
		svc.Append(threadID, agentID, Item{
			ID:      timelineID("turn-end", turnID),
			Kind:    "turn_end",
			Status:  status,
			AgentID: agentID,
			TurnID:  turnID,
		})
		if status == "failed" {
			diagnostic := util.FirstNonEmpty(
				strings.TrimSpace(ev.Error),
				strings.TrimSpace(ev.Reason),
				strings.TrimSpace(ev.Result),
				strings.TrimSpace(ev.Summary),
			)
			if diagnostic == "" {
				diagnostic = "turn failed without provider diagnostic"
			}
			appendErrorItem(
				svc,
				threadID,
				agentID,
				turnID,
				timelineID("error", "turn", turnID),
				diagnostic,
				ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
			)
		}
		emitTimelineUpdated(onUpdated, threadID)
	}
}

// reasoningDeltaHandler 将 reasoning stream 增量合并到同一个 thinking item。
// 非 reasoning stream 由其他投影链处理，避免 timeline 重复展示消息正文。
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
		// 同一 turn 的 reasoning 增量合并到单行，避免长推理过程刷出大量 timeline item。
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

// turnInterruptedHandler 追加 turn 被中断的 timeline 标记。
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

// itemStartedHandler 追加文件或命令 item 的运行中状态。
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

// toolCallBeginHandler 追加工具调用开始 item，并保存参数 preview。
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
			Preview:   previewText(observability.SafeToolArgumentsPreviewString(ev.ArgumentsPreview)),
			AgentID:   strings.TrimSpace(ev.AgentID),
			TurnID:    strings.TrimSpace(ev.TurnID),
			Ts:        ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
		})
		emitTimelineUpdated(onUpdated, threadID)
	}
}

// toolCallEndHandler 用工具结束事件完成已有 tool item，必要时走兼容恢复或兜底追加。
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

// recoverToolEndByCallID 兼容只回传 CallID、不回传 ToolName 的工具结束事件。
// 它会从线程 timeline 末尾反向查找匹配的 tool item，再用该 item 的工具名完成更新。
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

// approvalRequestedHandler 追加等待审批的 timeline item。
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

// approvalResolvedHandler 将审批 item 标记为 approved/rejected。
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
