package insight

import (
	"context"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/kelindar/event"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// NewInsightSubscribers 为 BusModule 声明 collector 总线订阅。
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
