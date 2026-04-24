package bus

import (
	"context"
	"errors"
	"sync"

	"github.com/kelindar/event"
)

// SubscriberSpec is the declarative BusModule-owned subscription contract.
// Business modules should provide this shape into group:"bus.subscribers"
// instead of registering bus callbacks from their own fx lifecycle hooks.
type SubscriberSpec struct {
	EventType     string
	HandlerSymbol string
	OwnerModule   string
	CancelOwner   string
	ShutdownClass string
	TestFixtureID string
	Register      func(*event.Dispatcher) context.CancelFunc
}

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
