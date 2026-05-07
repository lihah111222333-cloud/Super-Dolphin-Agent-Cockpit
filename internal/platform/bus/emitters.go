package bus

import "github.com/kelindar/event"

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
