package notify

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/orchestration"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// AgentAliasResolver maps (agentID, threadID) into a channel alias or
// empty when no alias applies. v1 has no per-agent notify binding yet,
// so the default resolver always returns empty ("drop"). When a future
// schema change lands (for example a column on agent_threads), the
// replacement is a single fx.Decorate away.
type AgentAliasResolver func(agentID, threadID string) string

// dropAllAliasResolver is the default resolver. It honours the P2
// plan's stance that core turn terminals 不能天然反推出通知目标; without
// an explicit opt-in alias source, we drop.
func dropAllAliasResolver(string, string) string { return "" }

// TurnNotifier bridges orchestration.NotifyTap -> contract.MessageNotifier.
// Every terminal event is resolved through AgentAliasResolver; empty
// alias means drop (plan-compliant). When an alias is present the
// notifier builds a contract.NotifyMessage and TryEnqueues — errors
// are logged + counted, never bubbled to the hook consumer.
type TurnNotifier struct {
	logger        *slog.Logger
	notifier      contract.MessageNotifier
	aliasResolver AgentAliasResolver

	skipped       atomic.Int64
	enqueued      atomic.Int64
	enqueueErrors atomic.Int64
}

// Compile-time check: TurnNotifier satisfies orchestration.NotifyTap.
var _ orchestration.NotifyTap = (*TurnNotifier)(nil)

// NewTurnNotifier wires a TurnNotifier with a resolver. A nil resolver
// falls back to dropAllAliasResolver so the tap is safely installed
// even when the deployment has no alias source wired in; every
// terminal still passes through the hook consumer unchanged and the
// skipped counter tracks the "no alias, dropped" branch.
// NewTurnNotifier 创建turnnotifier。
func NewTurnNotifier(logger *slog.Logger, notifier contract.MessageNotifier, resolver AgentAliasResolver) *TurnNotifier {
	if logger == nil {
		logger = pkglogger.Get()
	}
	if resolver == nil {
		resolver = dropAllAliasResolver
	}
	return &TurnNotifier{logger: logger, notifier: notifier, aliasResolver: resolver}
}

// OnTurnCompleted implements orchestration.NotifyTap.
// OnTurnCompleted 处理onturncompleted。
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

// OnTurnInterrupted implements orchestration.NotifyTap.
// OnTurnInterrupted 处理onturninterrupted。
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

// OnThreadStopped implements orchestration.NotifyTap.
// OnThreadStopped 处理on线程stopped。
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

// lookupAlias centralises trim + resolver delegation. Returning "" from
// the resolver (default behaviour) lines up with the P2 plan's
// drop/error policy for core turn terminals without an explicit alias.
func (t *TurnNotifier) lookupAlias(agentID, threadID string) string {
	if t == nil || t.aliasResolver == nil {
		return ""
	}
	return strings.TrimSpace(t.aliasResolver(strings.TrimSpace(agentID), strings.TrimSpace(threadID)))
}

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

// TurnMetrics mirrors the DAGNotifier surface for dashboards.
type TurnMetrics struct {
	Skipped       int64
	Enqueued      int64
	EnqueueErrors int64
}

// Metrics returns a point-in-time snapshot.
// Metrics 处理指标。
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

// buildTurnCompletedMessage renders a compact title + body for a
// TurnCompleted event. Raw payload fields (Result / Summary) are
// passed through unedited because the flusher's MarkdownEscape /
// NormalizeBody pipeline owns the escaping + truncation.
func buildTurnCompletedMessage(ev turndto.TurnCompleted) (title, body string) {
	return buildTurnCompletedTitle(ev), buildTurnCompletedBody(ev)
}

func buildTurnCompletedTitle(ev turndto.TurnCompleted) string {
	title := "Turn " + resolvedTurnCompletedStatus(ev)
	if id := strings.TrimSpace(ev.TurnID); id != "" {
		return title + ": " + id
	}
	return title
}

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

func buildTurnCompletedBody(ev turndto.TurnCompleted) string {
	parts := make([]string, 0, 5)
	parts = appendTurnCompletedField(parts, "Agent", ev.AgentID)
	parts = appendTurnCompletedField(parts, "Thread", ev.ThreadID)
	parts = appendTurnCompletedField(parts, "Stop reason", ev.StopReason)
	parts = appendTurnCompletedField(parts, "Error", ev.Error)
	return strings.Join(appendTurnCompletedResult(parts, ev), "\n")
}

func appendTurnCompletedField(parts []string, label, value string) []string {
	if value = strings.TrimSpace(value); value == "" {
		return parts
	}
	return append(parts, label+": "+value)
}

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
