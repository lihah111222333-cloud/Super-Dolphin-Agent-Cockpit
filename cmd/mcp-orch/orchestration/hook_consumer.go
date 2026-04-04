package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
)

const (
	hookTopicSessionStart = "agent.session.start"
	hookTopicStateChange  = "agent.state.change"
	hookTopicProcessExit  = "agent.process.exit"

	hookSyncTrigger = "hook_state_sync"

	hookRelayKindThreadStarted = "thread.started"
	hookRelayKindThreadStopped = "thread.stopped"
	hookRelayKindStateChanged  = "agent.state_changed"
)

type HookConsumer interface {
	After(ctx context.Context, payload mcp.HookPayload) (mcp.AfterDecision, error)
}

type hookConsumer struct {
	svc    *service
	logger *slog.Logger
}

type hookContextEnvelope struct {
	Kind  string          `json:"kind"`
	Event json.RawMessage `json:"event"`
}

func NewHookConsumer(svc *service, logger *slog.Logger) HookConsumer {
	return &hookConsumer{svc: svc, logger: loggerOrDefault(logger)}
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

	switch strings.TrimSpace(payload.Topic) {
	case hookTopicSessionStart:
		ev, ok := decodeHookEvent[threaddto.Started](c.logger, envelope, hookRelayKindThreadStarted)
		if ok {
			c.handleThreadStarted(ctx, ev)
		}
	case hookTopicStateChange:
		ev, ok := decodeHookEvent[agentdto.StateChanged](c.logger, envelope, hookRelayKindStateChanged)
		if ok {
			c.handleStateChanged(ctx, ev)
		}
	case hookTopicProcessExit:
		ev, ok := decodeHookEvent[threaddto.Stopped](c.logger, envelope, hookRelayKindThreadStopped)
		if ok {
			c.handleThreadStopped(ctx, ev)
		}
	}
	return decision, nil
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
