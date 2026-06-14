package bus

import "github.com/kelindar/event"

// Bus wraps *event.Dispatcher as the injected event bus.
type Bus struct {
	dispatcher *event.Dispatcher
}

// New 创建平台bus。
func New() *Bus {
	return &Bus{dispatcher: event.NewDispatcher()}
}

// NewDispatcher 创建调度器。
func NewDispatcher() *event.Dispatcher {
	return New().Dispatcher()
}

// Dispatcher 处理调度器。
func (b *Bus) Dispatcher() *event.Dispatcher {
	if b == nil {
		return nil
	}
	return b.dispatcher
}
