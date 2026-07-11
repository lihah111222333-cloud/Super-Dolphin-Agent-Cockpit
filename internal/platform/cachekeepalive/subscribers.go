package cachekeepalive

import (
	"context"
	"sync"

	"github.com/kelindar/event"
	platformbus "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// NewCacheKeepaliveSubscribers 声明 cachekeepalive 的 bus 订阅规格。
// cancel 由 SubscriberGroup 托管并用 sync.Once 包装，确保 fx 停止重入时只注销一次。
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
