package insight

import (
	"context"

	"github.com/kelindar/event"

	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// NewInsightSubscribers declares the collector bus subscriptions for BusModule.
func NewInsightSubscribers(c *collector, logger *pkglogger.Logger) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: platformbus.SubscriberSpec{
			EventType:     "turn.terminal",
			HandlerSymbol: "insight.collector.enqueueTerminal",
			OwnerModule:   "insight",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "insight-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				return c.subscribe(dispatcher, logger)
			},
		},
	}
}
