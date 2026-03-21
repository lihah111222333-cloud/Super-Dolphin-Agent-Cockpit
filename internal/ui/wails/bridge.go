package wails

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/eventsurface"
	"github.com/kelindar/event"
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
	b.cancels = eventsurface.Bind(b.dispatcher, b.logger, b.publish)
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
	for _, notification := range eventsurface.ExpandNotifications(method, payload) {
		normalized := payloadToMap(notification.Payload)
		b.lifecycle.EmitEvent(bridgeEventName, map[string]any{
			"type":    notification.Method,
			"payload": normalized,
		})
		b.emitCompatAgentEvent(notification.Method, normalized)
	}
}

func (b *EventBridge) emitCompatAgentEvent(method string, payload map[string]any) {
	if b == nil || b.lifecycle == nil {
		return
	}
	threadID := firstNonEmptyPayloadString(payload, "threadId", "thread_id", "agent_id", "agentId")
	if threadID == "" {
		return
	}
	b.lifecycle.EmitEvent(agentEventName, map[string]any{
		"agent_id": threadID,
		"type":     strings.TrimSpace(method),
		"payload":  payload,
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

func firstNonEmptyPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, _ := payload[key].(string)
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
