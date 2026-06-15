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

// Specs 处理specs。
func (g *SubscriberGroup) Specs() []SubscriberSpec {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]SubscriberSpec(nil), g.specs...)
}

// Start 启动平台bus流程。
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

// StopIntake 停止intake。
func (g *SubscriberGroup) StopIntake() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.intake = false
}

// Cancel 取消当前运行。
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

// CancelCount 处理cancelcount。
func (g *SubscriberGroup) CancelCount() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.cancels)
}
