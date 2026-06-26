// Package observation 实现 turn 观测层，将事件总线上的原始事件流归一化为六个
// 规范事实（terminal、token、timestamp、counts、call attribution、dedupe），
// 供轨迹收集、提炼和 UI 状态等消费者通过 Contract 接口读取。
package observation

import (
	"context"
	"sync"

	buscontract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

// NewObservationSubscribers 向 BusModule 声明 observation 的订阅规格，实际注册和关闭由 bus 统一管理。
func NewObservationSubscribers(contract Contract, logger *pkglogger.Logger) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: buscontract.SubscriberSpec{
			EventType:     "turn.observation",
			HandlerSymbol: "observation.Subscribe",
			OwnerModule:   "observation",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "observation-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				cancel := Subscribe(dispatcher, contract, logger)
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
