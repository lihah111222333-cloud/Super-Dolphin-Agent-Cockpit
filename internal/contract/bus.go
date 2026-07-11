package contract

import (
	"context"
	"log/slog"
	"reflect"

	"github.com/kelindar/event"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
)

// SubscriberSpec 是 BusModule 拥有的声明式订阅 contract。
// 业务模块通过 group:"bus.subscribers" 提供该结构，避免在各自 fx lifecycle 中直接注册 bus 回调。
type SubscriberSpec struct {
	EventType     string
	HandlerSymbol string
	OwnerModule   string
	CancelOwner   string
	ShutdownClass string
	TestFixtureID string
	Register      func(*event.Dispatcher) context.CancelFunc
}

// UISharedFilesChangedEmitter 是 shared-file 持久化向 UI 发布变更的窄输出边界。
// store 包只依赖该函数类型，不直接持有 event bus。
type UISharedFilesChangedEmitter func(uidto.UISharedFilesChanged)

// ResilientSubscribe 注册带 panic 恢复的事件订阅。
// nil dispatcher 或 handler 会返回 no-op cancel，真实 handler panic 会被记录而不打断 event bus。
func ResilientSubscribe[T event.Event](dispatcher *event.Dispatcher, fn func(T), logger *slog.Logger) context.CancelFunc {
	if dispatcher == nil || fn == nil {
		return func() {}
	}
	log := logger
	if log == nil {
		log = slog.Default()
	}
	return event.Subscribe(dispatcher, func(ev T) {
		if recovered := recoverCall(func() { fn(ev) }); recovered != nil {
			log.Error("handler panic", "type", eventTypeName(ev), "error", recovered)
		}
	})
}

// recoverCall 捕获订阅 handler 的 panic，供 ResilientSubscribe 记录错误。
func recoverCall(fn func()) (recovered any) {
	defer func() { recovered = recover() }()
	fn()
	return nil
}

// eventTypeName 返回事件类型名，nil 事件固定输出占位文本便于日志检索。
func eventTypeName(ev any) string {
	if ev == nil {
		return "<nil>"
	}
	return reflect.TypeOf(ev).String()
}

// NewEmitter 返回类型安全的事件发布函数。
// dispatcher 为空时发布被视为 no-op，方便可选 UI 事件出口在测试中复用。
func NewEmitter[T event.Event](dispatcher *event.Dispatcher) func(T) {
	return func(ev T) {
		if dispatcher == nil {
			return
		}
		event.Publish(dispatcher, ev)
	}
}
