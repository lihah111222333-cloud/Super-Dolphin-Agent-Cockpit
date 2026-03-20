package bus

import (
	"context"

	"github.com/kelindar/event"
)

// TypedEmitter is a type-safe view over Dispatcher.
type TypedEmitter[T event.Event] struct {
	dispatcher *event.Dispatcher
}

func NewTypedEmitter[T event.Event](dispatcher *event.Dispatcher) *TypedEmitter[T] {
	return &TypedEmitter[T]{dispatcher: dispatcher}
}

func (e *TypedEmitter[T]) Emit(ev T) {
	if e == nil || e.dispatcher == nil {
		return
	}
	event.Publish(e.dispatcher, ev)
}

func (e *TypedEmitter[T]) On(fn func(T)) context.CancelFunc {
	if e == nil || e.dispatcher == nil || fn == nil {
		return func() {}
	}
	return event.Subscribe(e.dispatcher, fn)
}
