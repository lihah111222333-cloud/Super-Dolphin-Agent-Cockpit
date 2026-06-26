// Package bus 提供基于 kelindar/event 的进程内事件总线，封装 Dispatcher 的创建、
// 订阅生命周期管理和结构化日志追踪。
package bus

import (
	"context"
	"log/slog"

	"github.com/kelindar/event"
)

// Router 管理一组订阅的生命周期，通过 Handle 收集 cancel 函数，Close 时统一注销。
type Router struct {
	subs *Subscription
}

// NewRouter 创建 Router；dispatcher 参数暂未使用，但保留旧构造签名以兼容调用方。
func NewRouter(_ *event.Dispatcher) *Router {
	return &Router{subs: NewSubscription()}
}

// Route 向 dispatcher 注册泛型事件处理函数，dispatcher 或 handler 为 nil 时跳过并记录警告。
func Route[T event.Event](dispatcher *event.Dispatcher, handler func(T)) context.CancelFunc {
	if dispatcher == nil || handler == nil {
		slog.Warn("bus: Route called with nil dispatcher or handler, subscription skipped",
			slog.Bool("dispatcher_nil", dispatcher == nil),
			slog.Bool("handler_nil", handler == nil),
		)
		return func() {}
	}
	return event.Subscribe(dispatcher, handler)
}

// Handle 将 cancel 函数注册到 Router，由 Close 统一调用。
// nil router 或 nil cancel 直接跳过，便于测试和可选订阅路径安全收口。
func (r *Router) Handle(cancel context.CancelFunc) {
	if r == nil || cancel == nil {
		return
	}
	r.subs.Add(cancel)
}

// Close 注销所有已注册订阅；重复 Close 在 Subscription 清空后不会再次触发 cancel。
func (r *Router) Close() {
	if r == nil || r.subs == nil {
		return
	}
	r.subs.CancelAll()
}
