package observation

import (
	"context"

	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

// NewObservationSubscribers declares the observation bus subscriptions for BusModule.
func NewObservationSubscribers(contract Contract, logger *pkglogger.Logger) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: platformbus.SubscriberSpec{
			EventType:     "turn.observation",
			HandlerSymbol: "observation.Subscribe",
			OwnerModule:   "observation",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "observation-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				return Subscribe(dispatcher, contract, logger)
			},
		},
	}
}
