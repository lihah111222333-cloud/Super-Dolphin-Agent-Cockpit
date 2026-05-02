package memory

import (
	"context"
	"errors"
	"sync"

	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolresults"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

var (
	ErrMemoryAlreadyExists     = errors.New("memory already exists")
	ErrMemoryLockTimeout       = errors.New("memory store lock timeout")
	ErrMemoryNotFound          = errors.New("memory not found")
	ErrInvalidMemoryEntry      = errors.New("invalid memory entry")
	ErrMemoryIndexUpdateFailed = errors.New("memory_index_update_failed")
)

type WriteOptions struct {
	SkipIndex bool
}

type MemoryWriteRequest struct {
	Name        string
	Description string
	Type        MemoryType
	Body        string
}

type memoryWriteGuard interface {
	ValidateWrite(path, content string) (string, error)
}

type memoryStructuredStore interface {
	Read(name string) (MemoryEntry, error)
	CreateStructured(req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error)
	UpdateStructured(req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error)
	// UpsertStructured writes the entry atomically inside a single disk
	// store lock acquisition (Phase 自有.1a). Replaces the legacy
	// Create-then-Update pattern in upsertStructuredMemory which had a
	// two-phase locking window where another writer could race in between
	// the failed Create and the follow-up Update, producing a lost update.
	UpsertStructured(req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error)
	Delete(name string, opts ...WriteOptions) error
}

type memoryWriteStore interface {
	memoryStructuredStore
	Root() string
	Create(entry MemoryEntry, opts ...WriteOptions) (MemoryEntry, error)
	Update(entry MemoryEntry, opts ...WriteOptions) (MemoryEntry, error)
}

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
		// Empty cache root (host without UserCacheDir nor TempDir) disables
		// persistedPath reads via NestedRuntime.SetToolReadCacheRoot's
		// empty-root contract — fail-closed.
		p.NestedRuntime.SetToolReadCacheRoot(toolresults.CacheDir())
	}
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
