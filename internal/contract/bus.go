package contract

import (
	"context"
	"log/slog"
	"reflect"

	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/kelindar/event"
)

// ---------------------------------------------------------------------------
// SubscriberSpec is the declarative BusModule-owned subscription contract.
// Business modules should provide this shape into group:"bus.subscribers"
// instead of registering bus callbacks from their own fx lifecycle hooks.
// ---------------------------------------------------------------------------
type SubscriberSpec struct {
	EventType     string
	HandlerSymbol string
	OwnerModule   string
	CancelOwner   string
	ShutdownClass string
	TestFixtureID string
	Register      func(*event.Dispatcher) context.CancelFunc
}

// UISharedFilesChangedEmitter is the narrow event output contract used by
// shared-file persistence without coupling store packages to the event bus.
type UISharedFilesChangedEmitter func(uidto.UISharedFilesChanged)

// ---------------------------------------------------------------------------
// ResilientSubscribe subscribes to events with panic recovery.
// ---------------------------------------------------------------------------
// ResilientSubscribe 处理resilientsubscribe。
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

func recoverCall(fn func()) (recovered any) {
	defer func() { recovered = recover() }()
	fn()
	return nil
}

func eventTypeName(ev any) string {
	if ev == nil {
		return "<nil>"
	}
	return reflect.TypeOf(ev).String()
}

// ---------------------------------------------------------------------------
// NewEmitter returns a typed publish function without hand-writing per-event
// wrappers.
// ---------------------------------------------------------------------------
// NewEmitter 创建emitter。
func NewEmitter[T event.Event](dispatcher *event.Dispatcher) func(T) {
	return func(ev T) {
		if dispatcher == nil {
			return
		}
		event.Publish(dispatcher, ev)
	}
}
