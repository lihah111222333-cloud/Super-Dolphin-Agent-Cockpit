package hooks

import (
	"context"
	"sync"

	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

// NewHooksRelaySubscribers declares hook relay bus subscriptions for BusModule.
// NewHooksRelaySubscribers 创建hooksrelaysubscribers。
func NewHooksRelaySubscribers(worker *hookDispatchWorker, logger *pkglogger.Logger) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: platformbus.SubscriberSpec{
			EventType:     "hooks.event.relay",
			HandlerSymbol: "hooks.startEventRelay",
			OwnerModule:   "hooks",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "hooks-relay-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				cancel := startEventRelay(dispatcher, worker, logger)
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
