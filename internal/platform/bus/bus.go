// Package bus 提供基于 kelindar/event 的进程内事件总线，封装 Dispatcher 的创建、
// 订阅生命周期管理和结构化日志追踪。
package bus

import "github.com/kelindar/event"

// Bus 封装 *event.Dispatcher，作为 fx 依赖注入的事件总线实例。
type Bus struct {
	dispatcher *event.Dispatcher // 底层 kelindar/event 调度器
}

// New 创建带独立 dispatcher 的进程内事件总线实例。
func New() *Bus {
	return &Bus{dispatcher: event.NewDispatcher()}
}

// NewDispatcher 为 fx 图提供裸 *event.Dispatcher。
// 运行时模块直接依赖 dispatcher，避免把 Bus 包装类型泄漏到业务层。
func NewDispatcher() *event.Dispatcher {
	return New().Dispatcher()
}

// Dispatcher 返回底层 *event.Dispatcher；nil receiver 表示总线未装配。
func (b *Bus) Dispatcher() *event.Dispatcher {
	if b == nil {
		return nil
	}
	return b.dispatcher
}
