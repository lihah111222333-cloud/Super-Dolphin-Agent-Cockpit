package wails

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/eventsurface"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// EventBridge 把后端 event surface 转发给 Wails 前端。
// Start/Stop 可能被 Fx 生命周期多次调用，因此 cancels 受 mutex 保护并保持幂等。
type EventBridge struct {
	dispatcher *event.Dispatcher
	lifecycle  *WailsLifecycle
	logger     *slog.Logger

	transitionMu sync.Mutex
	mu           sync.Mutex
	cancels      []context.CancelFunc
	generation   uint64
	active       bool
	inflight     int
	idle         chan struct{}
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
// 依赖或订阅为空时返回错误，让 Fx 阻断一个注定丢事件的桌面进程。
func (b *EventBridge) Start() error {
	if b == nil {
		return errors.New("bridge: event bridge is not configured")
	}
	if b.dispatcher == nil {
		return errors.New("bridge: event dispatcher is not configured")
	}
	if b.lifecycle == nil {
		return errors.New("bridge: Wails lifecycle is not configured")
	}
	if b.lifecycle.loadEmitter() == nil {
		return errors.New("bridge: Wails event emitter is not configured")
	}

	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	b.mu.Lock()
	if b.active {
		subscriptions := len(b.cancels)
		b.mu.Unlock()
		b.logger.Info("bridge: Start skipped (already started)", "cancels", subscriptions)
		return nil
	}
	b.generation++
	if b.generation == 0 {
		b.generation++
	}
	generation := b.generation
	b.active = true
	b.mu.Unlock()

	cancels := eventsurface.Bind(b.dispatcher, b.logger, func(method string, payload any) {
		if !b.beginPublish(generation) {
			return
		}
		defer b.endPublish()
		b.publish(method, payload)
	})
	if len(cancels) == 0 {
		b.deactivateAndWait()
		return errors.New("bridge: event subscriptions are empty")
	}

	b.mu.Lock()
	b.cancels = cancels
	b.mu.Unlock()
	b.logger.Info("bridge: started", "subscriptions", len(cancels), "generation", generation)
	return nil
}

// Stop 取消所有事件订阅，并允许后续 Start 重新绑定。
func (b *EventBridge) Stop() {
	if b == nil {
		return
	}

	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	b.mu.Lock()
	b.active = false
	cancels := b.cancels
	b.cancels = nil
	var idle <-chan struct{}
	if b.inflight > 0 {
		idle = b.idle
	}
	b.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	if idle != nil {
		<-idle
	}
	b.logger.Info("bridge: stopped", "subscriptions", len(cancels))
}

// beginPublish 只允许当前活跃 generation 的回调进入，并登记 in-flight 发布。
func (b *EventBridge) beginPublish(generation uint64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.active || b.generation != generation {
		return false
	}
	if b.inflight == 0 {
		b.idle = make(chan struct{})
	}
	b.inflight++
	return true
}

// endPublish 在最后一个回调离开时唤醒 Stop，保证 Stop 返回后没有旧事件继续写入前端。
func (b *EventBridge) endPublish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inflight <= 0 {
		return
	}
	b.inflight--
	if b.inflight == 0 && b.idle != nil {
		close(b.idle)
		b.idle = nil
	}
}

// deactivateAndWait 回滚一次未建立任何订阅的 Start。
func (b *EventBridge) deactivateAndWait() {
	b.mu.Lock()
	b.active = false
	var idle <-chan struct{}
	if b.inflight > 0 {
		idle = b.idle
	}
	b.mu.Unlock()
	if idle != nil {
		<-idle
	}
}

// publish 展开后端事件通知并发送给新旧前端事件通道。
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

// emitCompatAgentEvent 兼容旧前端监听的 agent-event 事件格式。
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

// firstNonEmptyPayloadString 按优先级读取第一个非空字符串字段。
func firstNonEmptyPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, _ := payload[key].(string)
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
