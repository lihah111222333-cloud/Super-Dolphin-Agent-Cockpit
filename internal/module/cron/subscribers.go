package cron

import (
	"context"
	"sync"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformbus "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// NewCronProgressSubscribers 声明 cron progress/terminal 事件订阅。
// 注册时创建单 worker 串行处理续租和终态写回；取消函数只执行一次，避免重复关闭 worker。
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
