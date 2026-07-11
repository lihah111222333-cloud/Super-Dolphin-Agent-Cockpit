// Package bus 提供基于 kelindar/event 的进程内事件总线，封装 Dispatcher 的创建、
// 订阅生命周期管理和结构化日志追踪。
package bus

import (
	"context"
	"errors"
	"sync"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// SubscriberSpec 保留 bus 包旧导入路径；订阅字段和跨模块 wire 定义以 contract 为准。
type SubscriberSpec = contract.SubscriberSpec

// SubscriberGroup 持有一组 SubscriberSpec，在 fx 生命周期 OnStart 时统一注册订阅，
// OnStop 时通过 Cancel 注销；intake 标志防止关闭后新增订阅。
type SubscriberGroup struct {
	dispatcher *event.Dispatcher
	specs      []SubscriberSpec

	mu      sync.Mutex
	intake  bool
	cancels []context.CancelFunc
}

var ErrSubscriberIntakeStopped = errors.New("bus subscriber intake stopped")

// Specs 返回当前注册的 SubscriberSpec 快照副本。
func (g *SubscriberGroup) Specs() []SubscriberSpec {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]SubscriberSpec(nil), g.specs...)
}

// Start 遍历所有 SubscriberSpec 并调用 Register 注册订阅；intake 已关闭时返回错误。
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

// StopIntake 关闭 intake 标志，之后不再接受新订阅注册。
func (g *SubscriberGroup) StopIntake() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.intake = false
}

// Cancel 注销所有已注册的订阅 cancel 函数，线程安全。
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

// CancelCount 返回当前已注册的 cancel 函数数量，用于测试和诊断。
func (g *SubscriberGroup) CancelCount() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.cancels)
}
