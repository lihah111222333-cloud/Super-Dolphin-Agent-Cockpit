package wails

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/eventsurface"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

// EventBridge 把后端 event surface 转发给 Wails 前端。
// Start/Stop 可能被 Fx 生命周期多次调用，因此 cancels 受 mutex 保护并保持幂等。
type EventBridge struct {
	dispatcher *event.Dispatcher
	lifecycle  *WailsLifecycle
	logger     *slog.Logger

	mu      sync.Mutex
	cancels []context.CancelFunc
}

// NewEventBridge 创建桌面事件桥，并把后端事件面绑定到 Wails 生命周期。
// logger 为空时使用全局 logger，其他依赖保持原样以便 Start 暴露装配问题。
func NewEventBridge(dispatcher *event.Dispatcher, lifecycle *WailsLifecycle, slogLogger *slog.Logger) *EventBridge {
	if slogLogger == nil {
		slogLogger = pkglogger.Get()
	}
	return &EventBridge{
		dispatcher: dispatcher,
		lifecycle:  lifecycle,
		logger:     slogLogger,
	}
}

// Start 绑定后端事件订阅；重复调用只保留第一次订阅。
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

// Stop 取消所有事件订阅，并允许后续 Start 重新绑定。
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

// publish 展开后端事件通知并发送给 Wails bridge-event 通道。
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
	}
}

// payloadToMap 将任意事件载荷规范化为 map，无法序列化时返回 error 字段。
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
