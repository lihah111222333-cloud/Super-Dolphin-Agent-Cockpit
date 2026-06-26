package rpc

import (
	"context"
	"sync"

	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

// NewRPCPushSubscribers 声明 RPC push 订阅规格，交给 BusModule 统一注册和取消。
// bridge 自带 dispatcher 时优先使用它，保证 push 与审批事件在同一事件总线上。
func NewRPCPushSubscribers(worker *pushNotificationWorker, bridge *PushBridge, logger *pkglogger.Logger) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: platformbus.SubscriberSpec{
			EventType:     "rpc.push.core",
			HandlerSymbol: "rpc.subscribeCoreEventPushes",
			OwnerModule:   "rpc",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "rpc-push-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				if bridge != nil && bridge.dispatcher != nil {
					dispatcher = bridge.dispatcher
				}
				cancels := subscribeCoreEventPushes(worker, dispatcher, logger)
				var once sync.Once
				return func() {
					once.Do(func() {
						for _, cancel := range cancels {
							if cancel != nil {
								cancel()
							}
						}
					})
				}
			},
		},
	}
}
