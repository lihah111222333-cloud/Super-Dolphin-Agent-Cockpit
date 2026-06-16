package insight

import (
	"context"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/kelindar/event"

	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
)

// NewInsightSubscribers declares the collector bus subscriptions for BusModule.
// NewInsightSubscribers 创建insightsubscribers。
func NewInsightSubscribers(c *collector, logger *pkglogger.Logger) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: contract.SubscriberSpec{
			EventType:     "turn.terminal",
			HandlerSymbol: "insight.collector.enqueueTerminal",
			OwnerModule:   "insight",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "insight-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				cancel := c.subscribe(dispatcher, logger)
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
