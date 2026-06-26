package hooks

import (
	"context"
	"sync"

	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

// NewHooksRelaySubscribers 声明 hooks relay 在 BusModule 中的订阅规格。
// 返回的 cancel 受 sync.Once 保护，确保重复 shutdown 不会重复取消底层 event 订阅。
func NewHooksRelaySubscribers(worker *hookDispatchWorker, logger *pkglogger.Logger) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: platformbus.SubscriberSpec{
			EventType:     "hooks.event.relay",
			HandlerSymbol: "hooks.startEventRelay",
			OwnerModule:   "hooks",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "hooks-relay-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				cancel := startEventRelay(dispatcher, worker, logger)
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
