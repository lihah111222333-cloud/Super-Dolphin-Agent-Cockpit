package bus

import (
	"context"
	"errors"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/kelindar/event"
)

// SubscriberSpec is a type alias kept for backward compatibility;
// the canonical definition lives in internal/contract.
type SubscriberSpec = contract.SubscriberSpec

type SubscriberGroup struct {
	dispatcher *event.Dispatcher
	specs      []SubscriberSpec

	mu      sync.Mutex
	intake  bool
	cancels []context.CancelFunc
}

var ErrSubscriberIntakeStopped = errors.New("bus subscriber intake stopped")

func (g *SubscriberGroup) Specs() []SubscriberSpec {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]SubscriberSpec(nil), g.specs...)
}

func (g *SubscriberGroup) Start() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.intake {
		return ErrSubscriberIntakeStopped
	}
	for _, spec := range g.specs {
		if spec.Register == nil {
			continue
		}
		cancel := spec.Register(g.dispatcher)
		if cancel != nil {
			g.cancels = append(g.cancels, cancel)
		}
	}
	return nil
}

func (g *SubscriberGroup) StopIntake() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.intake = false
}

func (g *SubscriberGroup) Cancel() {
	if g == nil {
		return
	}
	g.mu.Lock()
	cancels := append([]context.CancelFunc(nil), g.cancels...)
	g.cancels = nil
	g.mu.Unlock()
	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
}

func (g *SubscriberGroup) CancelCount() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.cancels)
}
