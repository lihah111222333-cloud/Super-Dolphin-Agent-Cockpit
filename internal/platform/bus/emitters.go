package bus

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/kelindar/event"
)

type domainEmitters struct {
	dispatcher *event.Dispatcher
}

func newDomainEmitters(dispatcher *event.Dispatcher) *domainEmitters {
	return &domainEmitters{dispatcher: dispatcher}
}

// Dispatcher 处理调度器。
func (e *domainEmitters) Dispatcher() *event.Dispatcher {
	if e == nil {
		return nil
	}
	return e.dispatcher
}

type ThreadEmitters struct{ *domainEmitters }

// NewThreadEmitters 创建线程emitters。
func NewThreadEmitters(dispatcher *event.Dispatcher) *ThreadEmitters {
	return &ThreadEmitters{domainEmitters: newDomainEmitters(dispatcher)}
}

// NewUISharedFilesChangedEmitter 创建UIshared文件changedemitter。
func NewUISharedFilesChangedEmitter(dispatcher *event.Dispatcher) contract.UISharedFilesChangedEmitter {
	return contract.NewEmitter[uidto.UISharedFilesChanged](dispatcher)
}
