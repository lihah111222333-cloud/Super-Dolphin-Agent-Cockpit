package bus

import (
	"context"
	"log/slog"

	"github.com/kelindar/event"
)

// Router only manages subscription lifecycles.
type Router struct {
	subs *Subscription
}

func NewRouter(_ *event.Dispatcher) *Router {
	return &Router{subs: NewSubscription()}
}

func Route[T event.Event](dispatcher *event.Dispatcher, handler func(T)) context.CancelFunc {
	if dispatcher == nil || handler == nil {
		slog.Warn("bus: Route called with nil dispatcher or handler, subscription skipped",
			slog.Bool("dispatcher_nil", dispatcher == nil),
			slog.Bool("handler_nil", handler == nil),
		)
		return func() {}
	}
	return event.Subscribe(dispatcher, handler)
}

func (r *Router) Handle(cancel context.CancelFunc) {
	if r == nil || cancel == nil {
		return
	}
	r.subs.Add(cancel)
}

func (r *Router) Close() {
	if r == nil || r.subs == nil {
		return
	}
	r.subs.CancelAll()
}
