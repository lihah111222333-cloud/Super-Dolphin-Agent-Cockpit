// Package bus 提供基于 kelindar/event 的进程内事件总线，封装 Dispatcher 的创建、
// 订阅生命周期管理和结构化日志追踪。
package bus

import "github.com/kelindar/event"

// Bus 封装 *event.Dispatcher，作为 fx 依赖注入的事件总线实例。
type Bus struct {
	dispatcher *event.Dispatcher // 底层 kelindar/event 调度器
}

// New 创建一个新的 Bus 实例，内部初始化 kelindar/event Dispatcher。
func New() *Bus {
	return &Bus{dispatcher: event.NewDispatcher()}
}

// NewDispatcher 创建并返回一个独立的 *event.Dispatcher，供 fx 直接注入使用。
func NewDispatcher() *event.Dispatcher {
	return New().Dispatcher()
}

// Dispatcher 返回底层 *event.Dispatcher；b 为 nil 时返回 nil。
func (b *Bus) Dispatcher() *event.Dispatcher {
	if b == nil {
		return nil
	}
	return b.dispatcher
}
