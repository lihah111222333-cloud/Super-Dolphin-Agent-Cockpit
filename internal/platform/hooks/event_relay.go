package hooks

import (
	"encoding/json"
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
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

// startEventRelay subscribes to bus events and forwards them to the
// hookDispatchWorker for serial DispatchAfter fanout. It returns a cancel
// function that unsubscribes all listeners; the worker lifecycle is owned
// separately by registerEventRelayLifecycle so OnStop can drain pending
// dispatches bounded by ctx.
//
// P22 P2 (hooks/event_relay fanout): the bus callback body intentionally
// contains no `go` / SafeGo / DispatchAfter call — only Enqueue.
// startEventRelay 启动事件relay。
func startEventRelay(dispatcher *event.Dispatcher, worker *hookDispatchWorker, logger *pkglogger.Logger) func() {
	if logger == nil {
		logger = pkglogger.Get()
	}
	startedCancel := platformbus.ResilientSubscribe(dispatcher, func(ev threaddto.Started) {
		enqueueHookDispatch(worker, TopicSessionStart, ev.Timestamp, mcp.HookPayload{
			AgentID:  strings.TrimSpace(ev.AgentID),
			ThreadID: strings.TrimSpace(ev.ThreadID),
			Context:  mustMarshalHookContext(logger, relayKindThreadStarted, ev),
		})
	}, logger)
	stoppedCancel := platformbus.ResilientSubscribe(dispatcher, func(ev threaddto.Stopped) {
		enqueueHookDispatch(worker, TopicProcessExit, ev.Timestamp, mcp.HookPayload{
			AgentID:  strings.TrimSpace(ev.AgentID),
			ThreadID: strings.TrimSpace(ev.ThreadID),
			Context:  mustMarshalHookContext(logger, relayKindThreadStopped, ev),
		})
	}, logger)
	stateCancel := platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.StateChanged) {
		enqueueHookDispatch(worker, TopicStateChange, ev.Timestamp, mcp.HookPayload{
			AgentID:  strings.TrimSpace(ev.AgentID),
			ThreadID: strings.TrimSpace(ev.ThreadID),
			Context:  mustMarshalHookContext(logger, relayKindStateChanged, ev),
		})
	}, logger)
	turnCompletedCancel := platformbus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
		enqueueHookDispatch(worker, TopicTurnAfter, ev.Timestamp, mcp.HookPayload{
			AgentID:  strings.TrimSpace(ev.AgentID),
			ThreadID: strings.TrimSpace(ev.ThreadID),
			Context:  mustMarshalHookContext(logger, relayKindTurnCompleted, ev),
		})
	}, logger)
	turnInterruptedCancel := platformbus.ResilientSubscribe(dispatcher, func(ev turndto.TurnInterrupted) {
		enqueueHookDispatch(worker, TopicTurnFailed, ev.Timestamp, mcp.HookPayload{
			AgentID:  strings.TrimSpace(ev.AgentID),
			ThreadID: strings.TrimSpace(ev.ThreadID),
			Context:  mustMarshalHookContext(logger, relayKindTurnInterrupted, ev),
		})
	}, logger)
	itemCompletedCancel := platformbus.ResilientSubscribe(dispatcher, func(ev turndto.ItemCompleted) {
		if !isFinalAnswerItemCompleted(ev) {
			return
		}
		enqueueHookDispatch(worker, TopicTurnProgress, ev.Timestamp, mcp.HookPayload{
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

// enqueueHookDispatch applies the pre-P2 validity filter (non-empty topic /
// agentID / context) before the request reaches the worker queue. Keeping
// the filter on the callback side means bad events never pay a queue
// slot, but it is still only cheap map-style checks and a worker Enqueue.
func enqueueHookDispatch(worker *hookDispatchWorker, topic string, timestamp time.Time, payload mcp.HookPayload) {
	if worker == nil || strings.TrimSpace(topic) == "" || strings.TrimSpace(payload.AgentID) == "" {
		return
	}
	if len(payload.Context) == 0 {
		return
	}
	worker.Enqueue(topic, timestamp, payload)
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
		return json.RawMessage("{}")
	}
	return raw
}

// isFinalAnswerItemCompleted 判断finalansweritemcompleted是否可用。
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
