package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	if p.NestedRuntime != nil {
		p.NestedRuntime.SetToolReadCacheRoot(nestedToolReadCacheRoot())
	}
	return newNestedIngestWorker(p.NestedRuntime, pkglogger.Get())
}

// nestedToolReadCacheRoot returns the persisted tool-result cache root that
// NestedRuntime contains its ToolCallEnd persistedPath reads against (P24
// cache-root-threading). The path is duplicated from
// internal/module/turn/tool_result_storage.go's toolResultStorageDir() because
// memory.Module sits below turn in the platform dependency order (see
// internal/app/modules.go) and importing turn here would invert it. Keep the
// two computations in lock-step. An empty return disables persistedPath reads
// outright via SetToolReadCacheRoot's empty-root contract — fail-closed if the
// host has neither UserCacheDir nor TempDir.
func nestedToolReadCacheRoot() string {
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	if strings.TrimSpace(base) == "" {
		return ""
	}
	return filepath.Join(base, "super-agent-v3", "tool-results")
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
