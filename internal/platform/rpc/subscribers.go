package rpc

import (
	"context"
	"sync"

	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

// NewRPCPushSubscribers declares RPC push bus subscriptions for BusModule.
// NewRPCPushSubscribers 创建RPCpushsubscribers。
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
