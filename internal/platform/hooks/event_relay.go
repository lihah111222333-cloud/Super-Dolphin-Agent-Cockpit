package hooks

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"go.uber.org/fx"
)

const (
	relayKindThreadStarted = "thread.started"
	relayKindThreadStopped = "thread.stopped"
	relayKindStateChanged  = "agent.state_changed"
	relayKindTurnCompleted = "turn.completed"
	relayKindTurnInterrupted = "turn.interrupted"
	relayKindTurnItemCompleted = "turn.item_completed"
)

type hookContextEnvelope struct {
	Kind  string          `json:"kind"`
	Event json.RawMessage `json:"event"`
}

func registerEventRelayLifecycle(lc fx.Lifecycle, in eventRelayIn) {
	if in.Dispatcher == nil || in.Manager == nil {
		return
	}
	logger := in.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}

	startedCancel := func() {}
	stoppedCancel := func() {}
	stateCancel := func() {}
	turnCompletedCancel := func() {}
	turnInterruptedCancel := func() {}
	itemCompletedCancel := func() {}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			startedCancel = platformbus.ResilientSubscribe(in.Dispatcher, func(ev threaddto.Started) {
				dispatchObservedAfter(in.Manager, logger, TopicSessionStart, ev.Timestamp, mcp.HookPayload{
					AgentID:  strings.TrimSpace(ev.AgentID),
					ThreadID: strings.TrimSpace(ev.ThreadID),
					Context:  mustMarshalHookContext(logger, relayKindThreadStarted, ev),
				})
			}, logger)
			stoppedCancel = platformbus.ResilientSubscribe(in.Dispatcher, func(ev threaddto.Stopped) {
				dispatchObservedAfter(in.Manager, logger, TopicProcessExit, ev.Timestamp, mcp.HookPayload{
					AgentID:  strings.TrimSpace(ev.AgentID),
					ThreadID: strings.TrimSpace(ev.ThreadID),
					Context:  mustMarshalHookContext(logger, relayKindThreadStopped, ev),
				})
			}, logger)
			stateCancel = platformbus.ResilientSubscribe(in.Dispatcher, func(ev agentdto.StateChanged) {
				dispatchObservedAfter(in.Manager, logger, TopicStateChange, ev.Timestamp, mcp.HookPayload{
					AgentID:  strings.TrimSpace(ev.AgentID),
					ThreadID: strings.TrimSpace(ev.ThreadID),
					Context:  mustMarshalHookContext(logger, relayKindStateChanged, ev),
				})
			}, logger)
			turnCompletedCancel = platformbus.ResilientSubscribe(in.Dispatcher, func(ev turndto.TurnCompleted) {
				dispatchObservedAfter(in.Manager, logger, TopicTurnAfter, ev.Timestamp, mcp.HookPayload{
					AgentID:  strings.TrimSpace(ev.AgentID),
					ThreadID: strings.TrimSpace(ev.ThreadID),
					Context:  mustMarshalHookContext(logger, relayKindTurnCompleted, ev),
				})
			}, logger)
			turnInterruptedCancel = platformbus.ResilientSubscribe(in.Dispatcher, func(ev turndto.TurnInterrupted) {
				dispatchObservedAfter(in.Manager, logger, TopicTurnFailed, ev.Timestamp, mcp.HookPayload{
					AgentID:  strings.TrimSpace(ev.AgentID),
					ThreadID: strings.TrimSpace(ev.ThreadID),
					Context:  mustMarshalHookContext(logger, relayKindTurnInterrupted, ev),
				})
			}, logger)
			itemCompletedCancel = platformbus.ResilientSubscribe(in.Dispatcher, func(ev turndto.ItemCompleted) {
				if !isFinalAnswerItemCompleted(ev) {
					return
				}
				dispatchObservedAfter(in.Manager, logger, TopicTurnProgress, ev.Timestamp, mcp.HookPayload{
					AgentID:  strings.TrimSpace(ev.AgentID),
					ThreadID: strings.TrimSpace(ev.ThreadID),
					Context:  mustMarshalHookContext(logger, relayKindTurnItemCompleted, ev),
				})
			}, logger)
			return nil
		},
		OnStop: func(context.Context) error {
			startedCancel()
			stoppedCancel()
			stateCancel()
			turnCompletedCancel()
			turnInterruptedCancel()
			itemCompletedCancel()
			return nil
		},
	})
}

func dispatchObservedAfter(manager *Manager, logger *pkglogger.Logger, topic string, timestamp time.Time, payload mcp.HookPayload) {
	if manager == nil || strings.TrimSpace(topic) == "" || strings.TrimSpace(payload.AgentID) == "" {
		return
	}
	if len(payload.Context) == 0 {
		return
	}
	go func() {
		ctx := sharedto.WithEventTime(context.Background(), timestamp)
		if _, err := manager.DispatchAfter(ctx, topic, payload); err != nil {
			logger.Warn("hooks: observed event relay failed",
				"topic", topic,
				"agent_id", payload.AgentID,
				"thread_id", payload.ThreadID,
				"error", err,
			)
		}
	}()
}

func mustMarshalHookContext(logger *pkglogger.Logger, kind string, event any) json.RawMessage {
	raw, err := json.Marshal(hookContextEnvelope{
		Kind:  strings.TrimSpace(kind),
		Event: mustMarshalHookEvent(event),
	})
	if err == nil {
		return raw
	}
	if logger != nil {
		logger.Warn("hooks: failed to marshal hook context", "kind", kind, "error", err)
	}
	return nil
}

func mustMarshalHookEvent(event any) json.RawMessage {
	raw, err := json.Marshal(event)
	if err != nil {
		return nil
	}
	return raw
}

func isFinalAnswerItemCompleted(ev turndto.ItemCompleted) bool {
	if !strings.EqualFold(strings.TrimSpace(ev.ItemType), "agentMessage") {
		return false
	}
	if len(ev.Payload) == 0 {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		return false
	}
	item, _ := payload["item"].(map[string]any)
	phase := firstHookPayloadString(item, "phase")
	if phase == "" {
		phase = firstHookPayloadString(payload, "phase")
	}
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case "final_answer", "final-answer", "finalanswer", "final":
		return true
	default:
		return false
	}
}

func firstHookPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if payload == nil {
			return ""
		}
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
