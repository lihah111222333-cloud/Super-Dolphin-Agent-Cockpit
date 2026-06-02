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

func (e *domainEmitters) Dispatcher() *event.Dispatcher {
	if e == nil {
		return nil
	}
	return e.dispatcher
}

type ThreadEmitters struct{ *domainEmitters }

func NewThreadEmitters(dispatcher *event.Dispatcher) *ThreadEmitters {
	return &ThreadEmitters{domainEmitters: newDomainEmitters(dispatcher)}
}

func NewUISharedFilesChangedEmitter(dispatcher *event.Dispatcher) contract.UISharedFilesChangedEmitter {
	return contract.NewEmitter[uidto.UISharedFilesChanged](dispatcher)
}
