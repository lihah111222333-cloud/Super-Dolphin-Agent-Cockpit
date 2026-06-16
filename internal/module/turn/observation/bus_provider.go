package observation

import (
	"context"
	"sync"

	buscontract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
	"github.com/kelindar/event"
)

// NewObservationSubscribers declares the observation bus subscriptions for BusModule.
// NewObservationSubscribers 创建observationsubscribers。
func NewObservationSubscribers(contract Contract, logger *pkglogger.Logger) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: buscontract.SubscriberSpec{
			EventType:     "turn.observation",
			HandlerSymbol: "observation.Subscribe",
			OwnerModule:   "observation",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "observation-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				cancel := Subscribe(dispatcher, contract, logger)
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
