package uistate

import (
	"context"
	"sync"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformbus "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
)

// NewUIStateSubscribers 声明 uistate 投影订阅，并把取消函数交给 bus.SubscriberGroup 管理。
// 返回的 cancel 使用 sync.Once 包装，避免 shutdown 重入时重复取消同一批订阅。
func NewUIStateSubscribers(svc *service) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: contract.SubscriberSpec{
			EventType:     "uistate.projections",
			HandlerSymbol: "uistate.registerProjectionSubscriptions",
			OwnerModule:   "uistate",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "uistate-projections-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				if svc == nil {
					return func() {}
				}
				svc.bindDispatcher(dispatcher)
				cancels := registerProjectionSubscriptions(dispatcher, svc)
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
