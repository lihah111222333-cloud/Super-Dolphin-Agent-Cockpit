package bus

import (
	"context"

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
