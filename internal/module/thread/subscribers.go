package thread

import (
	"context"
	"sync"

	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/kelindar/event"
)

// NewThreadSubscribers declares thread bus subscriptions for BusModule.
func NewThreadSubscribers(svc *service) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: platformbus.SubscriberSpec{
			EventType:     "thread.core",
			HandlerSymbol: "thread.registerThreadSubscriptions",
			OwnerModule:   "thread",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "thread-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				if svc != nil && dispatcher != nil {
					svc.bindDispatcher(dispatcher)
				}
				cancels := registerThreadSubscriptions(svc)
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
