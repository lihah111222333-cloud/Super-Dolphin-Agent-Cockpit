package toolbridge

import (
	"context"
	"sync"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/kelindar/event"
)

// NewToolbridgeDiffFallbackSubscribers 声明 ToolCallEnd 的 diff fallback 订阅。
// 返回的 cancel 使用 sync.Once 包装，避免总线重复 shutdown 时重复取消。
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
