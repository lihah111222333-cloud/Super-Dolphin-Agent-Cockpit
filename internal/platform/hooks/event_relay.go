package hooks

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

const (
	relayKindThreadStarted     = "thread.started"
	relayKindThreadStopped     = "thread.stopped"
	relayKindStateChanged      = "agent.state_changed"
	relayKindTurnCompleted     = "turn.completed"
	relayKindTurnInterrupted   = "turn.interrupted"
	relayKindTurnItemCompleted = "turn.item_completed"
)

type hookContextEnvelope struct {
	Kind  string          `json:"kind"`
	Event json.RawMessage `json:"event"`
}

// startEventRelay subscribes to bus events and relays them as hook
// dispatches.  It returns a cancel function that unsubscribes all listeners.
func startEventRelay(dispatcher *event.Dispatcher, manager *Manager, logger *pkglogger.Logger) func() {
	if logger == nil {
		logger = pkglogger.Get()
	}
	startedCancel := platformbus.ResilientSubscribe(dispatcher, func(ev threaddto.Started) {
		dispatchObservedAfter(manager, logger, TopicSessionStart, ev.Timestamp, mcp.HookPayload{
			AgentID:  strings.TrimSpace(ev.AgentID),
			ThreadID: strings.TrimSpace(ev.ThreadID),
			Context:  mustMarshalHookContext(logger, relayKindThreadStarted, ev),
		})
	}, logger)
	stoppedCancel := platformbus.ResilientSubscribe(dispatcher, func(ev threaddto.Stopped) {
		dispatchObservedAfter(manager, logger, TopicProcessExit, ev.Timestamp, mcp.HookPayload{
			AgentID:  strings.TrimSpace(ev.AgentID),
			ThreadID: strings.TrimSpace(ev.ThreadID),
			Context:  mustMarshalHookContext(logger, relayKindThreadStopped, ev),
		})
	}, logger)
	stateCancel := platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.StateChanged) {
		dispatchObservedAfter(manager, logger, TopicStateChange, ev.Timestamp, mcp.HookPayload{
			AgentID:  strings.TrimSpace(ev.AgentID),
			ThreadID: strings.TrimSpace(ev.ThreadID),
			Context:  mustMarshalHookContext(logger, relayKindStateChanged, ev),
		})
	}, logger)
	turnCompletedCancel := platformbus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
		dispatchObservedAfter(manager, logger, TopicTurnAfter, ev.Timestamp, mcp.HookPayload{
			AgentID:  strings.TrimSpace(ev.AgentID),
			ThreadID: strings.TrimSpace(ev.ThreadID),
			Context:  mustMarshalHookContext(logger, relayKindTurnCompleted, ev),
		})
	}, logger)
	turnInterruptedCancel := platformbus.ResilientSubscribe(dispatcher, func(ev turndto.TurnInterrupted) {
		dispatchObservedAfter(manager, logger, TopicTurnFailed, ev.Timestamp, mcp.HookPayload{
			AgentID:  strings.TrimSpace(ev.AgentID),
			ThreadID: strings.TrimSpace(ev.ThreadID),
			Context:  mustMarshalHookContext(logger, relayKindTurnInterrupted, ev),
		})
	}, logger)
	itemCompletedCancel := platformbus.ResilientSubscribe(dispatcher, func(ev turndto.ItemCompleted) {
		if !isFinalAnswerItemCompleted(ev) {
			return
		}
		dispatchObservedAfter(manager, logger, TopicTurnProgress, ev.Timestamp, mcp.HookPayload{
			AgentID:  strings.TrimSpace(ev.AgentID),
			ThreadID: strings.TrimSpace(ev.ThreadID),
			Context:  mustMarshalHookContext(logger, relayKindTurnItemCompleted, ev),
		})
	}, logger)
	return func() {
		startedCancel()
		stoppedCancel()
		stateCancel()
		turnCompletedCancel()
		turnInterruptedCancel()
		itemCompletedCancel()
	}
}

func dispatchObservedAfter(manager *Manager, logger *pkglogger.Logger, topic string, timestamp time.Time, payload mcp.HookPayload) {
	if manager == nil || strings.TrimSpace(topic) == "" || strings.TrimSpace(payload.AgentID) == "" {
		return
	}
	if len(payload.Context) == 0 {
		return
	}
	go func() {
		ctx := platformshared.WithEventTime(context.Background(), timestamp)
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
