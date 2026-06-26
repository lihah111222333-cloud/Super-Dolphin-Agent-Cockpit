// Package bus 提供基于 kelindar/event 的进程内事件总线，封装 Dispatcher 的创建、
// 订阅生命周期管理和结构化日志追踪。
package bus

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/kelindar/event"
)

// domainEmitters 持有 dispatcher 引用，作为各领域 emitter 的基础结构。
type domainEmitters struct {
	dispatcher *event.Dispatcher
}

// newDomainEmitters 封装 dispatcher 引用，供领域 emitter 共享同一事件总线。
func newDomainEmitters(dispatcher *event.Dispatcher) *domainEmitters {
	return &domainEmitters{dispatcher: dispatcher}
}

// Dispatcher 返回领域 emitter 使用的底层 dispatcher；nil receiver 表示 emitter 未装配。
func (e *domainEmitters) Dispatcher() *event.Dispatcher {
	if e == nil {
		return nil
	}
	return e.dispatcher
}

// ThreadEmitters 提供 Thread 领域事件的发射能力。
type ThreadEmitters struct{ *domainEmitters }

// NewThreadEmitters 为 Thread 领域提供基于同一 dispatcher 的事件发射器集合。
func NewThreadEmitters(dispatcher *event.Dispatcher) *ThreadEmitters {
	return &ThreadEmitters{domainEmitters: newDomainEmitters(dispatcher)}
}

// NewUISharedFilesChangedEmitter 暴露 UI shared-files 变更事件的跨模块 emitter。
func NewUISharedFilesChangedEmitter(dispatcher *event.Dispatcher) contract.UISharedFilesChangedEmitter {
	return contract.NewEmitter[uidto.UISharedFilesChanged](dispatcher)
}
