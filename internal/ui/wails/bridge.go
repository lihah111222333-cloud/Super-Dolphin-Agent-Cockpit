package wails

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/eventsurface"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

// EventBridge mirrors backend bus notifications into Wails UI events.
type EventBridge struct {
	dispatcher *event.Dispatcher
	lifecycle  *WailsLifecycle
	logger     *pkglogger.Logger

	mu      sync.Mutex
	cancels []context.CancelFunc
}

// NewEventBridge 创建事件桥接。
func NewEventBridge(dispatcher *event.Dispatcher, lifecycle *WailsLifecycle, slogLogger *pkglogger.Logger) *EventBridge {
	if slogLogger == nil {
		slogLogger = pkglogger.Get()
	}
	return &EventBridge{
		dispatcher: dispatcher,
		lifecycle:  lifecycle,
		logger:     slogLogger,
	}
}

// Start 启动桌面 UI 桥接流程。
func (b *EventBridge) Start() {
	if b == nil {
		return
	}
	if b.dispatcher == nil {
		b.logger.Warn("bridge: Start skipped", "nil_dispatcher", true)
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.cancels) > 0 {
		b.logger.Info("bridge: Start skipped (already started)", "cancels", len(b.cancels))
		return
	}
	b.cancels = eventsurface.Bind(b.dispatcher, b.logger, b.publish)
	b.logger.Info("bridge: started", "subscriptions", len(b.cancels))
}

// Stop 停止桌面 UI 桥接流程。
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
	notifications := eventsurface.ExpandNotifications(method, payload)
	for _, notification := range notifications {
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
