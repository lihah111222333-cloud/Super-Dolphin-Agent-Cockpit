package timeline

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
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

// turnCompletedHandler 只验证终态来自 canonical owner。
// UI 终态由 turn/terminal 事件独占，避免 timeline patch 覆盖 canonical terminal 行。
func turnCompletedHandler(_ Service, _ func(string)) func(turndto.TurnCompleted) {
	return func(ev turndto.TurnCompleted) {
		_, canonical, err := turndto.CanonicalTurnTerminal(ev)
		if err != nil || !canonical {
			return
		}
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
		updateKey := approvalUpdateKey(ev.SessionScope, ev.CallID, ev.RequestID)
		if threadID == "" || updateKey == "" {
			return
		}
		tool := strings.TrimSpace(ev.ToolName)
		svc.Append(threadID, strings.TrimSpace(ev.AgentID), Item{
			lookupKey:    updateKey,
			ID:           updateKey,
			Kind:         "approval",
			Status:       "pending",
			SessionScope: strings.TrimSpace(ev.SessionScope),
			CallID:       strings.TrimSpace(ev.CallID),
			RequestID:    ev.RequestID,
			Tool:         tool,
			ToolName:     tool,
			ItemType:     strings.TrimSpace(ev.Kind),
			AgentID:      strings.TrimSpace(ev.AgentID),
			TurnID:       strings.TrimSpace(ev.TurnID),
			Ts:           ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
		})
		emitTimelineUpdated(onUpdated, threadID)
	}
}

// approvalResolvedHandler 将审批 item 标记为 approved/rejected。
func approvalResolvedHandler(svc Service, onUpdated func(string)) func(tooldto.ToolApprovalResolved) {
	return func(ev tooldto.ToolApprovalResolved) {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID == "" {
			return
		}
		updateKey := approvalUpdateKey(ev.SessionScope, ev.CallID, ev.RequestID)
		status := "rejected"
		if ev.Approved {
			status = "approved"
		}
		updated := false
		if updateKey != "" {
			updated = svc.UpdateByCallID(threadID, strings.TrimSpace(ev.AgentID), updateKey, func(it *Item) {
				it.Kind = "approval"
				it.Status = status
				it.Done = true
				it.SessionScope = strings.TrimSpace(ev.SessionScope)
				it.CallID = strings.TrimSpace(ev.CallID)
				it.RequestID = ev.RequestID
				if strings.TrimSpace(it.Ts) == "" {
					it.Ts = ev.Timestamp.Format("2006-01-02T15:04:05Z07:00")
				}
				it.Tool = util.FirstNonEmpty(strings.TrimSpace(ev.ToolName), it.Tool)
				it.ToolName = util.FirstNonEmpty(strings.TrimSpace(ev.ToolName), it.ToolName)
			})
		}
		if updated {
			if onUpdated != nil {
				onUpdated(threadID)
			}
			return
		}
		terminalID := updateKey
		if terminalID == "" {
			terminalID = approvalTimelineID("approval-terminal", ev.SessionScope, ev.CallID, strconv.FormatInt(ev.RequestID, 10), ev.ToolName, ev.Kind, ev.Timestamp.Format(time.RFC3339Nano))
		}
		svc.Append(threadID, strings.TrimSpace(ev.AgentID), Item{
			lookupKey:    terminalID,
			ID:           terminalID,
			Kind:         "approval",
			Status:       status,
			SessionScope: strings.TrimSpace(ev.SessionScope),
			CallID:       strings.TrimSpace(ev.CallID),
			RequestID:    ev.RequestID,
			Tool:         strings.TrimSpace(ev.ToolName),
			ToolName:     strings.TrimSpace(ev.ToolName),
			ItemType:     strings.TrimSpace(ev.Kind),
			Done:         true,
			AgentID:      strings.TrimSpace(ev.AgentID),
			TurnID:       strings.TrimSpace(ev.TurnID),
			Ts:           ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
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

func approvalUpdateKey(sessionScope, callID string, requestID int64) string {
	sessionScope = strings.TrimSpace(sessionScope)
	callID = strings.TrimSpace(callID)
	if sessionScope == "" || callID == "" || requestID <= 0 {
		return ""
	}
	return approvalTimelineID("approval", sessionScope, callID, strconv.FormatInt(requestID, 10))
}

// approvalTimelineID 对每个身份分量做长度前缀编码，避免分隔符进入值时发生 tuple 碰撞。
func approvalTimelineID(parts ...string) string {
	var builder strings.Builder
	for _, part := range parts {
		part = strings.TrimSpace(part)
		builder.WriteString(strconv.Itoa(len(part)))
		builder.WriteByte(':')
		builder.WriteString(part)
	}
	return builder.String()
}
