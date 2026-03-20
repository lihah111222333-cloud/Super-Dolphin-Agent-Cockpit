package bus

import "github.com/kelindar/event"

// Bus wraps *event.Dispatcher as the injected event bus.
type Bus struct {
	dispatcher *event.Dispatcher
}

func New() *Bus {
	return &Bus{dispatcher: event.NewDispatcher()}
}

func NewDispatcher() *event.Dispatcher {
	return New().Dispatcher()
}

func (b *Bus) Dispatcher() *event.Dispatcher {
	if b == nil {
		return nil
	}
	return b.dispatcher
}
