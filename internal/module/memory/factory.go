package memory

import (
	"context"
	"sync"

	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

// NewMemorySubscribers declares memory lifecycle bus subscriptions for BusModule.
func NewMemorySubscribers(scheduler *autoDreamScheduler, nested *nestedIngestWorker, teamSync *teamSyncCoordinator, p memorySubscriberParams) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: platformbus.SubscriberSpec{
			EventType:     "memory.lifecycle",
			HandlerSymbol: "memory.registerLifecycleSubscriptions",
			OwnerModule:   "memory",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "memory-lifecycle-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				deps := memorySubscriptionDeps{
					Dispatcher:      dispatcher,
					Hooks:           p.Hooks,
					ContextProvider: p.ContextProvider,
					NestedRuntime:   p.NestedRuntime,
					ThreadStore:     p.ThreadStore,
					TeamSync:        p.TeamSync,
				}
				var cancels []context.CancelFunc
				appendCancel := func(cancel context.CancelFunc) {
					if cancel != nil {
						cancels = append(cancels, cancel)
					}
				}
				registerLifecycleSubscriptions(deps, scheduler, nested, teamSync, appendCancel)
				var once sync.Once
				return func() {
					once.Do(func() {
						cancelSubscriptions(cancels)
					})
				}
			},
		},
	}
}

func newAutoDreamSchedulerProvider(p autoDreamSchedulerProviderParams) *autoDreamScheduler {
	return newAutoDreamScheduler(p.Hooks, pkglogger.Get())
}

func newNestedIngestWorkerProvider(p nestedIngestWorkerProviderParams) *nestedIngestWorker {
	return newNestedIngestWorker(p.NestedRuntime, pkglogger.Get())
}

func newTeamSyncCoordinatorProvider(p teamSyncCoordinatorProviderParams) *teamSyncCoordinator {
	return newTeamSyncCoordinator(p.TeamSync, p.ThreadStore, pkglogger.Get())
}

func autoDreamSchedulerAsRunner(s *autoDreamScheduler) platformrunner.Runner {
	return platformrunner.AsRunner(s)
}

func nestedIngestWorkerAsRunner(w *nestedIngestWorker) platformrunner.Runner {
	return platformrunner.AsRunner(w)
}

func teamSyncCoordinatorAsRunner(c *teamSyncCoordinator) platformrunner.Runner {
	return platformrunner.AsRunner(c)
}
