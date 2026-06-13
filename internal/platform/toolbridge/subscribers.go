package toolbridge

import (
	"context"
	"sync"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/kelindar/event"
)

// NewToolbridgeDiffFallbackSubscribers declares the diff fallback subscription for BusModule.
// NewToolbridgeDiffFallbackSubscribers 创建toolbridgediff兜底subscribers。
func NewToolbridgeDiffFallbackSubscribers(tracker *diffFallbackTracker) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: platformbus.SubscriberSpec{
			EventType:     "toolbridge.diff.fallback",
			HandlerSymbol: "toolbridge.tracker.handleToolCallEnd",
			OwnerModule:   "toolbridge",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "toolbridge-diff-fallback-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				cancel := platformbus.ResilientSubscribe(dispatcher, func(ev tooldto.ToolCallEnd) {
					tracker.handleToolCallEnd(ev)
				}, tracker.logger)
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
