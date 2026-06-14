package cachekeepalive

import (
	"context"
	"sync"

	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

// NewCacheKeepaliveSubscribers declares cachekeepalive relay subscriptions for BusModule.
// NewCacheKeepaliveSubscribers 创建缓存keepalivesubscribers。
func NewCacheKeepaliveSubscribers(manager *Manager, logger *pkglogger.Logger) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: platformbus.SubscriberSpec{
			EventType:     "cache.keepalive.agent.launched",
			HandlerSymbol: "cachekeepalive.startKeepaliveRelay",
			OwnerModule:   "cachekeepalive",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "cachekeepalive-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				cancel := startKeepaliveRelay(dispatcher, manager, logger)
				var once sync.Once
				return func() {
					once.Do(func() {
						if cancel != nil {
							cancel()
						}
					})
				}
			},
		},
	}
}
