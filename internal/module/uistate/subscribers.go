package uistate

import (
	"context"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/kelindar/event"
)

// NewUIStateSubscribers declares UI state projection subscriptions for BusModule.
// NewUIStateSubscribers 创建UI状态subscribers。
func NewUIStateSubscribers(svc *service) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: contract.SubscriberSpec{
			EventType:     "uistate.projections",
			HandlerSymbol: "uistate.registerProjectionSubscriptions",
			OwnerModule:   "uistate",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "uistate-projections-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				if svc == nil {
					return func() {}
				}
				svc.bindDispatcher(dispatcher)
				cancels := registerProjectionSubscriptions(dispatcher, svc)
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
