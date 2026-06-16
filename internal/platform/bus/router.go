package bus

import (
	"context"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

// Router only manages subscription lifecycles.
type Router struct {
	subs *Subscription
}

// NewRouter 创建路由器。
func NewRouter(_ *event.Dispatcher) *Router {
	return &Router{subs: NewSubscription()}
}

// Route 处理route。
func Route[T event.Event](dispatcher *event.Dispatcher, handler func(T)) context.CancelFunc {
	if dispatcher == nil || handler == nil {
		pkglogger.Warn("bus: Route called with nil dispatcher or handler, subscription skipped",
			pkglogger.Any("dispatcher_nil", dispatcher == nil),
			pkglogger.Any("handler_nil", handler == nil),
		)
		return func() {}
	}
	return event.Subscribe(dispatcher, handler)
}

// Handle 处理平台bus请求。
func (r *Router) Handle(cancel context.CancelFunc) {
	if r == nil || cancel == nil {
		return
	}
	r.subs.Add(cancel)
}

// Close 关闭平台bus资源。
func (r *Router) Close() {
	if r == nil || r.subs == nil {
		return
	}
	r.subs.CancelAll()
}
