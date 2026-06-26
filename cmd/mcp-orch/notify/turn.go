package notify

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// AgentAliasResolver 把 agentID/threadID 映射为通知渠道别名。
// 返回空字符串表示显式丢弃；默认实现不推导渠道，避免 core turn 事件误发。
type AgentAliasResolver func(agentID, threadID string) string

// dropAllAliasResolver 是默认 alias 解析器。
// 没有显式 opt-in 渠道来源时，core turn 终态事件只计 skipped，不进入通知队列。
func dropAllAliasResolver(string, string) string { return "" }

// TurnNotifier 将 orchestration.NotifyTap 事件桥接到 contract.MessageNotifier。
// 每个终态事件都先经 AgentAliasResolver 解析渠道；入队失败只记录日志和计数，不反向阻断 hook consumer。
type TurnNotifier struct {
	logger        *slog.Logger
	notifier      contract.MessageNotifier
	aliasResolver AgentAliasResolver

	skipped       atomic.Int64
	enqueued      atomic.Int64
	enqueueErrors atomic.Int64
}

// 编译期确认 TurnNotifier 满足 orchestration.NotifyTap。
var _ orchestration.NotifyTap = (*TurnNotifier)(nil)

// NewTurnNotifier 装配 turn 通知 tap。
// resolver 为空时使用 dropAllAliasResolver，让未配置 alias 的部署只计 skipped，不影响 hook 消费链路。
func NewTurnNotifier(logger *slog.Logger, notifier contract.MessageNotifier, resolver AgentAliasResolver) *TurnNotifier {
	if logger == nil {
		logger = pkglogger.Get()
	}
	if resolver == nil {
		resolver = dropAllAliasResolver
	}
	return &TurnNotifier{logger: logger, notifier: notifier, aliasResolver: resolver}
}

// OnTurnCompleted 处理 turn 完成事件；无 alias 时按计划丢弃并计数。
func (t *TurnNotifier) OnTurnCompleted(ctx context.Context, ev turndto.TurnCompleted) {
	alias := t.lookupAlias(ev.AgentID, ev.ThreadID)
	if alias == "" {
		t.skipped.Add(1)
		return
	}
	title, body := buildTurnCompletedMessage(ev)
	level := contract.NotifyLevelInfo
	if !ev.Success || isNegativeStatus(ev.Status) {
		level = contract.NotifyLevelError
	}
	t.enqueue(ctx, alias, contract.NotifyMessage{Title: title, Body: body, Level: level})
}

// OnTurnInterrupted 处理 turn 中断事件，通知级别固定为 warn。
func (t *TurnNotifier) OnTurnInterrupted(ctx context.Context, ev turndto.TurnInterrupted) {
	alias := t.lookupAlias(ev.AgentID, ev.ThreadID)
	if alias == "" {
		t.skipped.Add(1)
		return
	}
	title := "Turn interrupted"
	if id := strings.TrimSpace(ev.TurnID); id != "" {
		title = "Turn interrupted: " + id
	}
	body := "Agent: " + strings.TrimSpace(ev.AgentID) +
		"\nThread: " + strings.TrimSpace(ev.ThreadID) +
		"\nReason: " + strings.TrimSpace(ev.Reason)
	t.enqueue(ctx, alias, contract.NotifyMessage{
		Title: title,
		Body:  body,
		Level: contract.NotifyLevelWarn,
	})
}

// OnThreadStopped 处理线程停止事件，当前只发信息级通知。
func (t *TurnNotifier) OnThreadStopped(ctx context.Context, ev threaddto.Stopped) {
	alias := t.lookupAlias(ev.AgentID, ev.ThreadID)
	if alias == "" {
		t.skipped.Add(1)
		return
	}
	title := "Agent stopped"
	if id := strings.TrimSpace(ev.AgentID); id != "" {
		title = "Agent stopped: " + id
	}
	body := "Thread: " + strings.TrimSpace(ev.ThreadID) +
		"\nReason: " + strings.TrimSpace(ev.Reason)
	t.enqueue(ctx, alias, contract.NotifyMessage{
		Title: title,
		Body:  body,
		Level: contract.NotifyLevelInfo,
	})
}

// lookupAlias 统一 trim 输入并委托 resolver。
// resolver 返回空字符串时表示没有明确通知目标，调用方按 skipped 处理。
func (t *TurnNotifier) lookupAlias(agentID, threadID string) string {
	if t == nil || t.aliasResolver == nil {
		return ""
	}
	return strings.TrimSpace(t.aliasResolver(strings.TrimSpace(agentID), strings.TrimSpace(threadID)))
}

// enqueue 向平台通知队列写入消息；失败只计数和日志，不向 hook 链路冒泡。
func (t *TurnNotifier) enqueue(ctx context.Context, alias string, msg contract.NotifyMessage) {
	if t.notifier == nil {
		t.enqueueErrors.Add(1)
		return
	}
	if err := t.notifier.TryEnqueue(ctx, contract.NotifyRequest{
		ChannelAlias: alias,
		Message:      msg,
	}); err != nil {
		t.enqueueErrors.Add(1)
		t.logger.Warn("notify(orch-turn): enqueue failed",
			slog.String("alias", alias),
			slog.String("error", err.Error()),
		)
		return
	}
	t.enqueued.Add(1)
}

// TurnMetrics 是 turn 通知器的只读计数器快照。
type TurnMetrics struct {
	Skipped       int64
	Enqueued      int64
	EnqueueErrors int64
}

// Metrics 返回 turn 通知计数器快照。
func (t *TurnNotifier) Metrics() TurnMetrics {
	if t == nil {
		return TurnMetrics{}
	}
	return TurnMetrics{
		Skipped:       t.skipped.Load(),
		Enqueued:      t.enqueued.Load(),
		EnqueueErrors: t.enqueueErrors.Load(),
	}
}

// buildTurnCompletedMessage 为 TurnCompleted 事件生成简短标题和正文。
// Result/Summary 原文交给 flusher 的 MarkdownEscape/NormalizeBody 管线统一转义和截断。
func buildTurnCompletedMessage(ev turndto.TurnCompleted) (title, body string) {
	return buildTurnCompletedTitle(ev), buildTurnCompletedBody(ev)
}

// buildTurnCompletedTitle 生成 turn 完成通知标题，优先带上 turn_id。
func buildTurnCompletedTitle(ev turndto.TurnCompleted) string {
	title := "Turn " + resolvedTurnCompletedStatus(ev)
	if id := strings.TrimSpace(ev.TurnID); id != "" {
		return title + ": " + id
	}
	return title
}

// resolvedTurnCompletedStatus 在事件未给 status 时从 Success 推导终态文案。
func resolvedTurnCompletedStatus(ev turndto.TurnCompleted) string {
	status := strings.TrimSpace(ev.Status)
	if status != "" {
		return status
	}
	if ev.Success {
		return "completed"
	}
	return "failed"
}

// buildTurnCompletedBody 组装 turn 完成通知正文，空字段会被跳过。
func buildTurnCompletedBody(ev turndto.TurnCompleted) string {
	parts := make([]string, 0, 5)
	parts = appendTurnCompletedField(parts, "Agent", ev.AgentID)
	parts = appendTurnCompletedField(parts, "Thread", ev.ThreadID)
	parts = appendTurnCompletedField(parts, "Stop reason", ev.StopReason)
	parts = appendTurnCompletedField(parts, "Error", ev.Error)
	return strings.Join(appendTurnCompletedResult(parts, ev), "\n")
}

// appendTurnCompletedField 追加非空的 label/value 行。
func appendTurnCompletedField(parts []string, label, value string) []string {
	if value = strings.TrimSpace(value); value == "" {
		return parts
	}
	return append(parts, label+": "+value)
}

// appendTurnCompletedResult 追加 Result/Summary/Message 三者中优先出现的正文。
func appendTurnCompletedResult(parts []string, ev turndto.TurnCompleted) []string {
	if v := strings.TrimSpace(ev.Result); v != "" {
		return append(parts, "Result:\n"+v)
	}
	if v := strings.TrimSpace(ev.Summary); v != "" {
		return append(parts, "Summary:\n"+v)
	}
	if v := strings.TrimSpace(ev.Message); v != "" {
		return append(parts, v)
	}
	return parts
}

// isNegativeStatus flags terminal statuses the plan maps to the
// error level. interrupted / aborted stay at info here because they
// are handled via OnTurnInterrupted with a warn level.
func isNegativeStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error":
		return true
	default:
		return false
	}
}
