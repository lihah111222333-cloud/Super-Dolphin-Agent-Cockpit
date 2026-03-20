package bus

import (
	"context"
	"sync"

	"github.com/kelindar/event"
)

type Projector[S any, E event.Event] struct {
	mu     sync.RWMutex
	state  S
	reduce func(S, E) S
}

func NewProjector[S any, E event.Event](initial S, reduce func(S, E) S) *Projector[S, E] {
	if reduce == nil {
		reduce = func(state S, _ E) S { return state }
	}
	return &Projector[S, E]{state: initial, reduce: reduce}
}

func (p *Projector[S, E]) Apply(ev E) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state = p.reduce(p.state, ev)
}

func (p *Projector[S, E]) State() S {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state
}

func (p *Projector[S, E]) Bind(dispatcher *event.Dispatcher) context.CancelFunc {
	if p == nil {
		return func() {}
	}
	return Route(dispatcher, p.Apply)
}
