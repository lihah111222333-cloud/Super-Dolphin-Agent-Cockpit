package uistate

import (
	"context"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/kelindar/event"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate/terminalstatus"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate/timeline"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
)

// registerProjectionSubscriptions 将 UI 投影订阅挂到事件总线，并返回统一取消函数列表。
// timeline 更新会在持锁状态下先生成 patch，再释放给事件发送方，避免漏掉同一线程的增量。
func registerProjectionSubscriptions(dispatcher *event.Dispatcher, svc *service) []context.CancelFunc {
	cancels := []context.CancelFunc{
		contract.ResilientSubscribe(dispatcher, svc.applyAgentStateChanged, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyAgentLaunched, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyAgentStopped, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyAgentRecovering, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyAgentFailed, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyAgentRuntimeReported, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyThreadStarted, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyThreadUpdated, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyThreadStopped, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyTurnStarted, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyTurnInterrupted, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyTurnCompleted, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyTurnResumed, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyTurnInputReceived, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyTurnOutputDelta, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyItemStarted, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyItemCompleted, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyToolCallBegin, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyToolCallEnd, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyToolDiffUpdated, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyToolApprovalRequested, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyToolApprovalResolved, svc.logger),
		contract.ResilientSubscribe(dispatcher, svc.applyTokensUpdated, svc.logger),
	}
	onTimelineUpdated := func(threadID string) {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			return
		}
		svc.mu.Lock()
		patch := svc.threadPatchLocked(threadID, "timeline/updated")
		ev := svc.projectionUpdatedLocked("timeline")
		svc.mu.Unlock()
		ev.ThreadID = threadID
		svc.emitThreadPatchEvent(patch)
		svc.emitProjectionUpdatedEvents(ev)
	}
	if svc.timeline != nil {
		cancels = append(cancels, timeline.RegisterSubscriptions(dispatcher, svc.timeline, svc.logger, onTimelineUpdated)...)
	}
	return cancels
}

// applyTokensUpdated 将 token 统计事件投影到全局和线程维度。
// TotalTokens 缺失时用输入/输出合计补齐，避免前端进度条没有分母。
func (s *service) applyTokensUpdated(ev uidto.UITokensUpdated) {
	threadID := strings.TrimSpace(ev.ThreadID)
	applyMutation(s, threadID, func() {
		used := ev.TotalTokens
		if used <= 0 {
			used = ev.InputTokens + ev.OutputTokens
		}
		var pct float64
		if ev.ContextWindowTokens > 0 && used > 0 {
			pct = float64(used) / float64(ev.ContextWindowTokens) * 100
			if pct > 100 {
				pct = 100
			}
		}
		tokenUsage := TokenUsage{
			InputTokens:         ev.InputTokens,
			OutputTokens:        ev.OutputTokens,
			TotalTokens:         ev.TotalTokens,
			UsedTokens:          used,
			ContextWindowTokens: ev.ContextWindowTokens,
			UsedPercent:         pct,
		}
		s.state.TokenUsage = tokenUsage
		if s.state.TokenUsages == nil {
			s.state.TokenUsages = make(map[string]TokenUsage)
		}
		s.state.TokenUsages[threadID] = tokenUsage
	}, func() uidto.UIThreadPatch {
		patch := s.threadPatchLocked(threadID, "thread/tokenusage/updated")
		patch.TokenUsage = tokenUsagePatch(ev)
		return patch
	})
}

// applyItemStarted 根据 item 类型增加线程活动深度和活动统计。
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
		stats := s.threadActivityStatsLocked(threadID)
		switch activity {
		case "editing":
			rt.editDepth++
			stats.FileEdits++
		case "command":
			rt.commandDepth++
			stats.Commands++
		}
	}, func() uidto.UIThreadPatch {
		if !ok {
			return uidto.UIThreadPatch{}
		}
		return s.refreshThreadPatchLocked(threadID, agentID, "item/started")
	})
}

// applyItemCompleted 根据 item 类型回落线程活动深度。
// 深度不会降到负数，避免乱序完成事件破坏 sidebar 状态。
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

// applyToolCallBegin 将工具开始事件投影到线程活动状态和统计计数。
// LSP 计数会先归一化工具名，以兼容 provider 前缀和旧 lsp_* 名称。
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
		stats := s.threadActivityStatsLocked(threadID)
		toolName := strings.TrimSpace(ev.ToolName)
		if toolName != "" {
			if stats.ToolCalls == nil {
				stats.ToolCalls = map[string]int64{}
			}
			stats.ToolCalls[toolName]++
			// LSPCalls 统计归一化后的 LSP 工具名，避免 provider 前缀或旧名称影响 UI 计数。
			if isLSPActivityTool(normalizeToolName(toolName)) {
				stats.LSPCalls++
			}
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

// applyToolCallEnd 根据工具结束事件回落协作或工具活动深度。
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

// applyToolApprovalRequested 增加审批等待深度，并为终端输入请求设置 overlay。
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

// applyToolApprovalResolved 回落审批等待深度，并在终端输入完成后清理 overlay。
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

// completedTurnSummary 合并 active turn 和完成事件，生成 recent turn 摘要。
// active turn 中已有开始时间会保留，完成事件只覆盖结果状态和错误字段。
func completedTurnSummary(current *TurnSummary, ev turndto.TurnCompleted) TurnSummary {
	summary := TurnSummary{
		ID:          strings.TrimSpace(ev.TurnID),
		AgentID:     strings.TrimSpace(ev.AgentID),
		ThreadID:    strings.TrimSpace(ev.ThreadID),
		Status:      completionStatus(ev),
		Error:       strings.TrimSpace(ev.Error),
		Reason:      strings.TrimSpace(ev.Reason),
		CompletedAt: clone.Time(&ev.Timestamp),
	}
	if current != nil && current.ID == summary.ID {
		summary = *cloneTurn(current)
		summary.Status = completionStatus(ev)
		summary.Error = strings.TrimSpace(ev.Error)
		summary.Reason = strings.TrimSpace(ev.Reason)
		summary.CompletedAt = clone.Time(&ev.Timestamp)
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
	return terminalstatus.Status(ev.Success, ev.Status, ev.Reason, ev.Error)
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

func chooseTime(next, current *time.Time) *time.Time {
	if next != nil && !next.IsZero() {
		return clone.Time(next)
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
