package memory

import (
	"context"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// drainMemoryHooks preserves the legacy drain order for focused tests and
// fallback callers. Production ownership is split: scheduler / nested /
// teamSync stop through their RunnerModule adapters, while the legacy dream
// task is closed by bindMemoryDrainShutdown during resource shutdown.
func drainMemoryHooks(ctx context.Context, scheduler *autoDreamScheduler, nested *nestedIngestWorker, teamSync *teamSyncCoordinator, hooks *MemoryLifecycleHooks) {
	drainStoppable(ctx, "auto-dream scheduler", scheduler)
	drainStoppable(ctx, "nested ingest worker", nested)
	drainStoppable(ctx, "team sync coordinator", teamSync)
	drainDreamTask(ctx, hooks)
}

type stoppable interface{ Stop(context.Context) error }

func drainStoppable(ctx context.Context, name string, s stoppable) {
	if s == nil {
		return
	}
	if err := s.Stop(ctx); err != nil && !errors.Is(err, context.Canceled) {
		pkglogger.Get().Warn("memory: "+name+" drain failed", "error", err)
	}
}

func drainDreamTask(ctx context.Context, hooks *MemoryLifecycleHooks) {
	if hooks == nil {
		return
	}
	hooks.killDreamTask()
	waitCtx := ctx
	if waitCtx == nil {
		waitCtx = context.Background()
	}
	if err := hooks.waitDreamTask(waitCtx); err != nil && !errors.Is(err, context.Canceled) {
		pkglogger.Get().Warn("memory: dream task drain failed", "error", err)
	}
}

func registerLifecycleSubscriptions(p memorySubscriptionDeps, scheduler *autoDreamScheduler, nested *nestedIngestWorker, teamSync *teamSyncCoordinator, hookWorker *memoryHookWorker, appendCancel func(context.CancelFunc)) {
	registerThreadHookSubscriptions(p, nested, teamSync, hookWorker, appendCancel)
	registerBackgroundExtractionSubscriptions(p, appendCancel)
	registerContextProviderSubscriptions(p, appendCancel)
	registerAutoDreamSubscriptions(p, scheduler, appendCancel)
}

func registerBackgroundExtractionSubscriptions(p memorySubscriptionDeps, appendCancel func(context.CancelFunc)) {
	if p.Hooks == nil || !p.Hooks.extractOnStop {
		return
	}
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnStarted) {
		p.Hooks.onTurnStarted(ev)
	}, pkglogger.Get()))
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnInterrupted) {
		p.Hooks.onTurnTerminated(ev.ThreadID, ev.TurnID)
	}, pkglogger.Get()))
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnStalled) {
		p.Hooks.onTurnTerminated(ev.ThreadID, ev.TurnID)
	}, pkglogger.Get()))
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev tooldto.ToolCallBegin) {
		p.Hooks.onToolCallBegin(ev)
	}, pkglogger.Get()))
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev tooldto.ToolDiffUpdated) {
		p.Hooks.onToolDiffUpdated(ev)
	}, pkglogger.Get()))
}

// registerThreadHookSubscriptions 注册线程hooksubscriptions。
func registerThreadHookSubscriptions(p memorySubscriptionDeps, nested *nestedIngestWorker, teamSync *teamSyncCoordinator, hookWorker *memoryHookWorker, appendCancel func(context.CancelFunc)) {
	if p.NestedRuntime != nil {
		appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev threaddto.Started) {
			p.NestedRuntime.OnThreadStart(ev.ThreadID)
		}, pkglogger.Get()))
		// P22 P2 Finding 10: the ToolCallEnd callback must stay off the
		// synchronous nested-read slow-path; nestedIngestWorker owns the
		// lossless pending-set + wake-signal and invokes AddToolReadResult
		// (which os.ReadFile's the persisted result) on its own goroutine.
		if nested != nil {
			appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev tooldto.ToolCallEnd) {
				nested.Enqueue(ev.ThreadID, ev.ToolName, ev.Result, ev.PersistedPath)
			}, pkglogger.Get()))
		}
	}
	if p.TeamSync != nil {
		registerTeamSyncSubscriptions(p, teamSync, appendCancel)
	}
	if p.Hooks == nil || !p.Hooks.enabled {
		return
	}
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev threaddto.Started) {
		// Callback only updates in-memory state (no I/O); ctx unused by callee.
		p.Hooks.onThreadStart(context.Background(), ev)
	}, pkglogger.Get()))
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnInputReceived) {
		// Callback enqueues to memoryHookWorker; disk I/O (deleteIntent /
		// writeDetectedIntent) runs on the worker goroutine, not here.
		hookWorker.Enqueue(memoryHookRequest{
			kind:      memoryHookTurnInputReceived,
			turnInput: ev,
		})
	}, pkglogger.Get()))
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnCompleted) {
		// Callback enqueues to memoryHookWorker; disk I/O (onTurnEnd,
		// intent detection, background extraction) runs on the worker
		// goroutine, not here.
		hookWorker.Enqueue(memoryHookRequest{
			kind:          memoryHookTurnCompleted,
			turnCompleted: ev,
		})
	}, pkglogger.Get()))
}

// registerTeamSyncSubscriptions is the P22 P2 Finding 5/6 boundary: the bus
// callback does nothing more than forward the event to the
// teamSyncCoordinator. Every slow-path bit (ThreadStore.GetByThreadID,
// git resolveRepoSlug, remote pull/push, fsnotify watcher spawn) runs on
// the coordinator's worker goroutine, not on the dispatcher callback.
func registerTeamSyncSubscriptions(p memorySubscriptionDeps, coordinator *teamSyncCoordinator, appendCancel func(context.CancelFunc)) {
	if coordinator == nil {
		return
	}
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev threaddto.Started) {
		coordinator.EnqueueStart(ev)
	}, pkglogger.Get()))
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev threaddto.Stopped) {
		coordinator.EnqueueStop(ev)
	}, pkglogger.Get()))
}

func registerContextProviderSubscriptions(p memorySubscriptionDeps, appendCancel func(context.CancelFunc)) {
	if p.ContextProvider == nil {
		return
	}
	registerTurnTerminationSubscriptions(p, appendCancel)
}

func registerTurnTerminationSubscriptions(p memorySubscriptionDeps, appendCancel func(context.CancelFunc)) {
	term := func(threadID, turnID string) { p.ContextProvider.onTurnTerminated(threadID, turnID) }
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnCompleted) { term(ev.ThreadID, ev.TurnID) }, pkglogger.Get()))
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnInterrupted) { term(ev.ThreadID, ev.TurnID) }, pkglogger.Get()))
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnStalled) { term(ev.ThreadID, ev.TurnID) }, pkglogger.Get()))
}

func cancelSubscriptions(cancels []context.CancelFunc) {
	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
}
