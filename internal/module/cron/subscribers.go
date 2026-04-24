package cron

import (
	"context"
	"sync"

	"github.com/kelindar/event"

	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// NewCronProgressSubscribers declares cron progress subscriptions for BusModule.
func NewCronProgressSubscribers(scheduler *Scheduler, logger *pkglogger.Logger) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: platformbus.SubscriberSpec{
			EventType:     "cron.progress",
			HandlerSymbol: "cron.subscribeCronProgress",
			OwnerModule:   "cron",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "cron-progress-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				cancelProgress := subscribeCronProgress(dispatcher, scheduler, logger)
				cancelTerminal := subscribeCronTerminalEvents(dispatcher, scheduler, logger)
				var once sync.Once
				return func() {
					once.Do(func() {
						if cancelProgress != nil {
							cancelProgress()
						}
						if cancelTerminal != nil {
							cancelTerminal()
						}
					})
				}
			},
		},
	}
}
