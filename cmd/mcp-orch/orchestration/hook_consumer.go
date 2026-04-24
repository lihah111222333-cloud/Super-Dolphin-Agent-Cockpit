package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	hookTopicSessionStart = "agent.session.start"
	hookTopicStateChange  = "agent.state.change"
	hookTopicTurnAfter    = "agent.turn.after"
	hookTopicTurnFailed   = "agent.turn.failed"
	hookTopicTurnProgress = "agent.turn.progress"
	hookTopicProcessExit  = "agent.process.exit"

	hookSyncTrigger = "hook_state_sync"

	hookRelayKindThreadStarted     = "thread.started"
	hookRelayKindThreadStopped     = "thread.stopped"
	hookRelayKindStateChanged      = "agent.state_changed"
	hookRelayKindTurnCompleted     = "turn.completed"
	hookRelayKindTurnInterrupted   = "turn.interrupted"
	hookRelayKindTurnItemCompleted = "turn.item_completed"
)

// NotifyTap observes core-turn terminal events coming through the hook
// consumer chain. Implementations forward into the orch MessageNotifier
// (cmd/mcp-orch/notify) or any other side-channel. The tap is called
// synchronously on the hook-dispatch goroutine, so every method must
// be non-blocking — the expected implementation is a TryEnqueue onto
// a bounded channel and immediately return.
//
// A nil NotifyTap on the hookConsumer is treated as no-op. Handlers
// fire the tap after the existing report / state handling completes so
// an errant tap cannot disrupt the consumer's primary path.
type NotifyTap interface {
	OnTurnCompleted(ctx context.Context, ev turndto.TurnCompleted)
	OnTurnInterrupted(ctx context.Context, ev turndto.TurnInterrupted)
	OnThreadStopped(ctx context.Context, ev threaddto.Stopped)
}

type hookConsumer struct {
	svc       *service
	logger    *slog.Logger
	notifyTap NotifyTap
}

type hookContextEnvelope struct {
	Kind  string          `json:"kind"`
	Event json.RawMessage `json:"event"`
}

// newHookConsumer keeps the pre-existing 2-arg signature for in-package
// tests (notably the hook_consumer_test suite). Wire-level fx injection
// uses HookAfterHandlerParams + ProvideHookAfterHandler so the optional
// NotifyTap can be discovered without breaking these tests.
//
// P22 P4 S4c2: the previously exported HookConsumer interface,
// NewHookConsumer constructor, and ProvideHookConsumer provider formed
// a subpackage bootstrap-hook protocol shell. Root assembly now wires
// the handler via contract.BootstrapHookAfterHandler, so this package
// no longer exports the interface or constructor. The archtest in
// internal/archtest/orchestration_no_hookconsumer_export_guard_test.go
// locks this in place.
func newHookConsumer(svc *service, logger *slog.Logger) *hookConsumer {
	return newHookConsumerInternal(svc, logger, nil)
}

// HookAfterHandlerParams is the fx.In bundle for ProvideHookAfterHandler.
// A nil NotifyTap is valid (treated as no-op), so downstream modules
// that do not register a tap — or unit tests that wire the orchestration
// providers without the orch notify module — boot cleanly.
type HookAfterHandlerParams struct {
	fx.In

	Service   *service
	Logger    *slog.Logger `optional:"true"`
	NotifyTap NotifyTap    `optional:"true"`
}

// ProvideHookAfterHandler is the fx-facing constructor. It returns the
// bootstrap-side after-hook as a plain function (contract.BootstrapHookAfterHandler)
// so the root assembly plumbs it straight into bootstrap.HookConfig.OnAfter
// without typing on any orchestration subpackage interface.
func ProvideHookAfterHandler(p HookAfterHandlerParams) contract.BootstrapHookAfterHandler {
	return newHookConsumerInternal(p.Service, p.Logger, p.NotifyTap).After
}

func newHookConsumerInternal(svc *service, logger *slog.Logger, tap NotifyTap) *hookConsumer {
	return &hookConsumer{svc: svc, logger: loggerOrDefault(logger), notifyTap: tap}
}

func (c *hookConsumer) After(ctx context.Context, payload mcp.HookPayload) (mcp.AfterDecision, error) {
	decision := mcp.AfterDecision{Decision: mcp.HookDecisionApprove}
	if c == nil || c.svc == nil {
		return decision, nil
	}
	envelope, ok := decodeHookContextEnvelope(c.logger, payload.Context)
	if !ok {
		return decision, nil
	}

	c.dispatchAfterTopic(ctx, strings.TrimSpace(payload.Topic), envelope)
	return decision, nil
}

func (c *hookConsumer) dispatchAfterTopic(ctx context.Context, topic string, envelope hookContextEnvelope) {
	switch topic {
	case hookTopicSessionStart:
		c.handleSessionStartTopic(ctx, envelope)
	case hookTopicStateChange:
		c.handleStateChangeTopic(ctx, envelope)
	case hookTopicTurnAfter:
		c.handleTurnAfterTopic(ctx, envelope)
	case hookTopicTurnFailed:
		c.handleTurnFailedTopic(ctx, envelope)
	case hookTopicTurnProgress:
		c.handleTurnProgressTopic(ctx, envelope)
	case hookTopicProcessExit:
		c.handleProcessExitTopic(ctx, envelope)
	}
}

func (c *hookConsumer) handleSessionStartTopic(ctx context.Context, envelope hookContextEnvelope) {
	if ev, ok := decodeHookEvent[threaddto.Started](c.logger, envelope, hookRelayKindThreadStarted); ok {
		c.handleThreadStarted(ctx, ev)
	}
}

func (c *hookConsumer) handleStateChangeTopic(ctx context.Context, envelope hookContextEnvelope) {
	if ev, ok := decodeHookEvent[agentdto.StateChanged](c.logger, envelope, hookRelayKindStateChanged); ok {
		c.handleStateChanged(ctx, ev)
	}
}

func (c *hookConsumer) handleTurnAfterTopic(ctx context.Context, envelope hookContextEnvelope) {
	if ev, ok := decodeHookEvent[turndto.TurnCompleted](c.logger, envelope, hookRelayKindTurnCompleted); ok {
		c.handleTurnCompleted(ctx, ev)
	}
}

func (c *hookConsumer) handleTurnFailedTopic(ctx context.Context, envelope hookContextEnvelope) {
	if ev, ok := decodeHookEvent[turndto.TurnInterrupted](c.logger, envelope, hookRelayKindTurnInterrupted); ok {
		c.handleTurnInterrupted(ctx, ev)
	}
}

func (c *hookConsumer) handleTurnProgressTopic(ctx context.Context, envelope hookContextEnvelope) {
	if ev, ok := decodeHookEvent[turndto.ItemCompleted](c.logger, envelope, hookRelayKindTurnItemCompleted); ok {
		c.handleItemCompleted(ctx, ev)
	}
}

func (c *hookConsumer) handleProcessExitTopic(ctx context.Context, envelope hookContextEnvelope) {
	if ev, ok := decodeHookEvent[threaddto.Stopped](c.logger, envelope, hookRelayKindThreadStopped); ok {
		c.handleThreadStopped(ctx, ev)
	}
}

func (c *hookConsumer) handleThreadStarted(ctx context.Context, ev threaddto.Started) {
	provider := normalizeRuntimeProvider(ev.Provider)
	err := c.svc.withAgentLocked(ev.AgentID, func(agent *agentRuntime) error {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID != "" {
			agent.threadID = threadID
			agent.remoteThreadID = threadID
		}
		beforeProvider, beforeProviderSource := snapshotProvider(agent)
		applyRuntimeReportLocked(agent, 0, provider)
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
		afterProvider, afterProviderSource := snapshotProvider(agent)
		if beforeProvider != afterProvider || beforeProviderSource != afterProviderSource {
			c.svc.publishAgentRuntimeReported(agent)
		}
		return nil
	})
	c.logUnexpectedHookError("thread started", ev.AgentID, ev.ThreadID, err)
}

func (c *hookConsumer) handleStateChanged(ctx context.Context, ev agentdto.StateChanged) {
	nextState := strings.TrimSpace(ev.NewState)
	if !isKnownMirroredState(nextState) {
		c.logger.Warn("orchestration: ignoring unknown mirrored agent state",
			"agent_id", ev.AgentID,
			"thread_id", ev.ThreadID,
			"state", nextState,
		)
		return
	}
	err := c.svc.withAgentLocked(ev.AgentID, func(agent *agentRuntime) error {
		before := strings.TrimSpace(agent.state)
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID != "" {
			agent.threadID = threadID
			agent.remoteThreadID = threadID
		}
		if nextState == agentdto.StateIdle || nextState == agentdto.StateStopped || nextState == agentdto.StateFailed {
			agent.activeTurnID = ""
		}
		if before != nextState {
			agent.state = nextState
		}
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
		if before != nextState {
			c.svc.publishStateChanged(agent, before, hookSyncTrigger)
		}
		return nil
	})
	c.logUnexpectedHookError("state change", ev.AgentID, ev.ThreadID, err)
}

func (c *hookConsumer) handleThreadStopped(ctx context.Context, ev threaddto.Stopped) {
	err := c.svc.withAgentLocked(ev.AgentID, func(agent *agentRuntime) error {
		before := strings.TrimSpace(agent.state)
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID != "" {
			agent.threadID = threadID
			agent.remoteThreadID = threadID
		}
		agent.activeTurnID = ""
		agent.stopReason = strings.TrimSpace(ev.Reason)
		agent.state = agentdto.StateStopped
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
		c.svc.publishStateChanged(agent, before, hookSyncTrigger)
		c.svc.publishAgentStopped(agent, ev.Reason)
		return nil
	})
	c.logUnexpectedHookError("thread stopped", ev.AgentID, ev.ThreadID, err)
	if c.notifyTap != nil {
		c.notifyTap.OnThreadStopped(ctx, ev)
	}
}

func (c *hookConsumer) handleTurnCompleted(ctx context.Context, ev turndto.TurnCompleted) {
	if c == nil || c.svc == nil {
		return
	}
	report := platformshared.FirstTrimmed(ev.Result, ev.Summary, ev.Message)
	_, err := c.svc.HandleReportEvent(withEventTime(ctx, ev.Timestamp), ReportEvent{
		AgentID:   strings.TrimSpace(ev.AgentID),
		Report:    report,
		EventType: "turn/completed",
		EventData: mustMarshalHookReportEvent(ev),
	})
	c.logUnexpectedHookError("turn completed report", ev.AgentID, ev.ThreadID, err)
	handleTurnCompletedEvent(c.svc, c.logger, ev)
	if c.notifyTap != nil {
		c.notifyTap.OnTurnCompleted(ctx, ev)
	}
}

func (c *hookConsumer) handleTurnInterrupted(ctx context.Context, ev turndto.TurnInterrupted) {
	handleTurnInterruptedEvent(c.svc, c.logger, ev)
	if c.notifyTap != nil {
		c.notifyTap.OnTurnInterrupted(ctx, ev)
	}
}

func (c *hookConsumer) handleItemCompleted(ctx context.Context, ev turndto.ItemCompleted) {
	if c == nil || c.svc == nil || !isFinalAnswerItem(ev) {
		return
	}
	_, err := c.svc.HandleReportEvent(withEventTime(ctx, ev.Timestamp), ReportEvent{
		AgentID:   strings.TrimSpace(ev.AgentID),
		EventType: strings.TrimSpace(platformshared.FirstTrimmed(ev.RawType, "item/completed")),
		EventData: append(json.RawMessage(nil), ev.Payload...),
	})
	c.logUnexpectedHookError("turn item completed", ev.AgentID, ev.ThreadID, err)
}

func (c *hookConsumer) logUnexpectedHookError(action, agentID, threadID string, err error) {
	if err == nil || errors.Is(err, errAgentNotFound) {
		return
	}
	c.logger.Warn("orchestration: hook consumer failed",
		"action", action,
		"agent_id", strings.TrimSpace(agentID),
		"thread_id", strings.TrimSpace(threadID),
		"error", err,
	)
}

func decodeHookContextEnvelope(logger *slog.Logger, raw json.RawMessage) (hookContextEnvelope, bool) {
	if len(raw) == 0 {
		return hookContextEnvelope{}, false
	}
	var envelope hookContextEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		loggerOrDefault(logger).Warn("orchestration: failed to decode hook context", "error", err)
		return hookContextEnvelope{}, false
	}
	if strings.TrimSpace(envelope.Kind) == "" || len(envelope.Event) == 0 {
		return hookContextEnvelope{}, false
	}
	return envelope, true
}

func decodeHookEvent[T any](logger *slog.Logger, envelope hookContextEnvelope, wantKind string) (T, bool) {
	var zero T
	if strings.TrimSpace(envelope.Kind) != strings.TrimSpace(wantKind) {
		return zero, false
	}
	var event T
	if err := json.Unmarshal(envelope.Event, &event); err != nil {
		loggerOrDefault(logger).Warn("orchestration: failed to decode hook event",
			"kind", envelope.Kind,
			"error", err,
		)
		return zero, false
	}
	return event, true
}

func mustMarshalHookReportEvent(event any) json.RawMessage {
	raw, err := json.Marshal(event)
	if err != nil {
		return nil
	}
	return raw
}

func isFinalAnswerItem(ev turndto.ItemCompleted) bool {
	if !strings.EqualFold(strings.TrimSpace(ev.ItemType), "agentMessage") || len(ev.Payload) == 0 {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		return false
	}
	item, _ := payload["item"].(map[string]any)
	phase := firstPayloadString(item, "phase")
	if phase == "" {
		phase = firstPayloadString(payload, "phase")
	}
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case "final_answer", "final-answer", "finalanswer", "final":
		return true
	default:
		return false
	}
}

func firstPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if payload == nil {
			return ""
		}
		value, ok := payload[key].(string)
		if ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isKnownMirroredState(state string) bool {
	switch strings.TrimSpace(state) {
	case agentdto.StateProvisioning,
		agentdto.StateIdle,
		agentdto.StateTurnQueued,
		agentdto.StateTurnStarting,
		agentdto.StateTurnRunning,
		agentdto.StateAwaitingUserInput,
		agentdto.StateRecovering,
		agentdto.StateStopping,
		agentdto.StateStopped,
		agentdto.StateFailed:
		return true
	default:
		return false
	}
}
