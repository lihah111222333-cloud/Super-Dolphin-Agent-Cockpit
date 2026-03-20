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

type AgentEmitters struct{ *domainEmitters }

type TurnEmitters struct{ *domainEmitters }

type ToolEmitters struct{ *domainEmitters }

type TaskEmitters struct{ *domainEmitters }

type WorkspaceEmitters struct{ *domainEmitters }

type UIEmitters struct{ *domainEmitters }

// NewEmitter returns a typed publish function without hand-writing per-event wrappers.
func NewEmitter[T event.Event](dispatcher *event.Dispatcher) func(T) {
	return func(ev T) {
		if dispatcher == nil {
			return
		}
		event.Publish(dispatcher, ev)
	}
}

func NewAgentEmitters(dispatcher *event.Dispatcher) *AgentEmitters {
	return &AgentEmitters{domainEmitters: newDomainEmitters(dispatcher)}
}

func NewTurnEmitters(dispatcher *event.Dispatcher) *TurnEmitters {
	return &TurnEmitters{domainEmitters: newDomainEmitters(dispatcher)}
}

func NewToolEmitters(dispatcher *event.Dispatcher) *ToolEmitters {
	return &ToolEmitters{domainEmitters: newDomainEmitters(dispatcher)}
}

func NewTaskEmitters(dispatcher *event.Dispatcher) *TaskEmitters {
	return &TaskEmitters{domainEmitters: newDomainEmitters(dispatcher)}
}

func NewWorkspaceEmitters(dispatcher *event.Dispatcher) *WorkspaceEmitters {
	return &WorkspaceEmitters{domainEmitters: newDomainEmitters(dispatcher)}
}

func NewUIEmitters(dispatcher *event.Dispatcher) *UIEmitters {
	return &UIEmitters{domainEmitters: newDomainEmitters(dispatcher)}
}
