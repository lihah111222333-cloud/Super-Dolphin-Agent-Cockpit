package cron

import (
	"context"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/kelindar/event"

	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
)

// NewCronProgressSubscribers declares cron progress subscriptions for BusModule.
// NewCronProgressSubscribers 创建cronprogresssubscribers。
func NewCronProgressSubscribers(scheduler *Scheduler, logger *pkglogger.Logger) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: contract.SubscriberSpec{
			EventType:     "cron.progress",
			HandlerSymbol: "cron.subscribeCronProgress",
			OwnerModule:   "cron",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "cron-progress-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				worker := newCronProgressWorker(scheduler, logger)
				worker.Start()
				cancelProgress := subscribeCronProgress(dispatcher, worker, logger)
				cancelTerminal := subscribeCronTerminalEvents(dispatcher, worker, logger)
				var once sync.Once
				return func() {
					once.Do(func() {
						if cancelProgress != nil {
							cancelProgress()
						}
						if cancelTerminal != nil {
							cancelTerminal()
						}
						_ = worker.Stop(context.Background())
					})
				}
			},
		},
	}
}
