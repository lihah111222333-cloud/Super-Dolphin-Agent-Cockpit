package mcpcontrol

import (
	"context"
	"sync"

	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

// NewMCPConfigChangeSubscribers 声明 MCP 配置变更总线订阅，并用 sync.Once 保证取消函数只执行一次。
func NewMCPConfigChangeSubscribers(worker *configFanoutWorker, logger *pkglogger.Logger) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: platformbus.SubscriberSpec{
			EventType:     "mcpcontrol.config.change",
			HandlerSymbol: "mcpcontrol.registerConfigChangeSubscriptions",
			OwnerModule:   "mcpcontrol",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "mcpcontrol-config-change-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				cancels := registerConfigChangeSubscriptions(dispatcher, worker, logger)
				var once sync.Once
				return func() {
					once.Do(func() {
						for _, cancel := range cancels {
							if cancel != nil {
								cancel()
							}
						}
					})
				}
			},
		},
	}
}
