package wails

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/kelindar/event"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
)

const (
	methodEventAgentStateChanged = "ui/state/changed"
	methodEventTurnStarted       = "turn/started"
	methodEventTurnCompleted     = "turn/completed"
)

type EventBridge struct {
	dispatcher *event.Dispatcher
	lifecycle  *WailsLifecycle
	logger     *slog.Logger

	mu      sync.Mutex
	cancels []context.CancelFunc
}

func NewEventBridge(dispatcher *event.Dispatcher, lifecycle *WailsLifecycle, logger *slog.Logger) *EventBridge {
	if logger == nil {
		logger = slog.Default()
	}
	return &EventBridge{
		dispatcher: dispatcher,
		lifecycle:  lifecycle,
		logger:     logger,
	}
}

func (b *EventBridge) Start() {
	if b == nil || b.dispatcher == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.cancels) > 0 {
		return
	}

	b.cancels = []context.CancelFunc{
		platformbus.ResilientSubscribe(b.dispatcher, func(ev agentdto.StateChanged) {
			b.publish(methodEventAgentStateChanged, ev)
		}, b.logger),
		platformbus.ResilientSubscribe(b.dispatcher, func(ev turndto.TurnStarted) {
			b.publish(methodEventTurnStarted, ev)
		}, b.logger),
		platformbus.ResilientSubscribe(b.dispatcher, func(ev turndto.TurnCompleted) {
			b.publish(methodEventTurnCompleted, ev)
		}, b.logger),
	}
}

func (b *EventBridge) Stop() {
	if b == nil {
		return
	}

	b.mu.Lock()
	cancels := b.cancels
	b.cancels = nil
	b.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
}

func (b *EventBridge) publish(method string, payload any) {
	if b == nil || b.lifecycle == nil {
		return
	}
	b.lifecycle.EmitEvent(bridgeEventName, map[string]any{
		"type":    method,
		"payload": payloadToMap(payload),
	})
}

func payloadToMap(payload any) map[string]any {
	switch typed := payload.(type) {
	case nil:
		return map[string]any{}
	case map[string]any:
		return typed
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return map[string]any{"error": err.Error()}
	}
	return result
}
