package insight

import (
	"context"
	"sync"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformbus "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
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
