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

// newDomainEmitters 创建持有 dispatcher 的 domainEmitters 实例。
func newDomainEmitters(dispatcher *event.Dispatcher) *domainEmitters {
	return &domainEmitters{dispatcher: dispatcher}
}

// Dispatcher 返回底层 *event.Dispatcher；e 为 nil 时返回 nil。
func (e *domainEmitters) Dispatcher() *event.Dispatcher {
	if e == nil {
		return nil
	}
	return e.dispatcher
}

// ThreadEmitters 提供 Thread 领域事件的发射能力。
type ThreadEmitters struct{ *domainEmitters }

// NewThreadEmitters 创建 ThreadEmitters，注入 dispatcher。
func NewThreadEmitters(dispatcher *event.Dispatcher) *ThreadEmitters {
	return &ThreadEmitters{domainEmitters: newDomainEmitters(dispatcher)}
}

// NewUISharedFilesChangedEmitter 创建 UI 共享文件变更事件的 Emitter。
func NewUISharedFilesChangedEmitter(dispatcher *event.Dispatcher) contract.UISharedFilesChangedEmitter {
	return contract.NewEmitter[uidto.UISharedFilesChanged](dispatcher)
}
