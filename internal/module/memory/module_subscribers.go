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

// drainMemoryHooks 按固定顺序关闭记忆相关后台组件。
// 生产路径由各自 RunnerModule 接管生命周期；这里保留给聚焦测试和兜底关闭路径，
// 保证 scheduler、nested、team sync、dream task 的 drain 顺序一致。
func drainMemoryHooks(ctx context.Context, scheduler *autoDreamScheduler, nested *nestedIngestWorker, teamSync *teamSyncCoordinator, hooks *MemoryLifecycleHooks) {
	drainStoppable(ctx, "auto-dream scheduler", scheduler)
	drainStoppable(ctx, "nested ingest worker", nested)
	drainStoppable(ctx, "team sync coordinator", teamSync)
	drainDreamTask(ctx, hooks)
}

type stoppable interface{ Stop(context.Context) error }

// drainStoppable 调用组件 Stop 并记录非取消类错误。
// 关闭流程不在这里聚合错误，避免单个后台组件阻断其它记忆资源释放。
func drainStoppable(ctx context.Context, name string, s stoppable) {
	if s == nil {
		return
	}
	if err := s.Stop(ctx); err != nil && !errors.Is(err, context.Canceled) {
		pkglogger.Get().Warn("memory: "+name+" drain failed", "error", err)
	}
}

// drainDreamTask 终止旧 dream task 并等待退出。
// ctx 为 nil 时使用 Background，调用方若需要关闭超时必须传入带 deadline 的上下文。
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

// registerLifecycleSubscriptions 统一注册记忆模块对 thread、turn、tool 和 prompt 事件的订阅。
// 各子函数只保存取消函数；真正的慢路径必须交给 worker 或后台组件处理。
func registerLifecycleSubscriptions(p memorySubscriptionDeps, scheduler *autoDreamScheduler, nested *nestedIngestWorker, teamSync *teamSyncCoordinator, hookWorker *memoryHookWorker, appendCancel func(context.CancelFunc)) {
	registerThreadHookSubscriptions(p, nested, teamSync, hookWorker, appendCancel)
	registerBackgroundExtractionSubscriptions(p, appendCancel)
	registerContextProviderSubscriptions(p, appendCancel)
	registerAutoDreamSubscriptions(p, scheduler, appendCancel)
}

// registerBackgroundExtractionSubscriptions 订阅后台抽取所需的 turn/tool 事件。
// 这些回调只更新内存追踪表，不读取历史或写记忆，避免阻塞事件分发器。
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

// registerThreadHookSubscriptions 注册 thread 生命周期与记忆 hook 订阅。
// 回调只做内存状态更新或入队，所有读盘、远端同步和记忆写入都在专用 worker 上执行。
func registerThreadHookSubscriptions(p memorySubscriptionDeps, nested *nestedIngestWorker, teamSync *teamSyncCoordinator, hookWorker *memoryHookWorker, appendCancel func(context.CancelFunc)) {
	if p.NestedRuntime != nil {
		appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev threaddto.Started) {
			p.NestedRuntime.OnThreadStart(ev.ThreadID)
		}, pkglogger.Get()))
		// ToolCallEnd 回调不能直接进入 nested 读盘慢路径；worker 负责维护 pending
		// 集合和 wake 信号，并在自己的 goroutine 中调用 AddToolReadResult。
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
		// 回调只更新内存状态，不做 I/O；被调方不依赖事件上下文。
		p.Hooks.onThreadStart(context.Background(), ev)
	}, pkglogger.Get()))
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnInputReceived) {
		// 回调只入队；显式 remember/forget 可能写盘，必须在 memoryHookWorker 上执行。
		hookWorker.Enqueue(memoryHookRequest{
			kind:      memoryHookTurnInputReceived,
			turnInput: ev,
		})
	}, pkglogger.Get()))
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnCompleted) {
		// 回调只入队；turn 结束处理、意图识别和后台抽取启动都由 worker 串行执行。
		hookWorker.Enqueue(memoryHookRequest{
			kind:          memoryHookTurnCompleted,
			turnCompleted: ev,
		})
	}, pkglogger.Get()))
}

// registerTeamSyncSubscriptions 把 thread start/stop 转交给团队同步协调器。
// 总线回调只入队；线程存储查询、仓库识别、远端 pull/push 和 watcher 启停都在协调器
// worker 中完成，避免阻塞 dispatcher。
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

// registerContextProviderSubscriptions 注册相关记忆上下文的 turn 终止清理。
// 未启用 ContextProvider 时直接跳过，避免空订阅占用 dispatcher。
func registerContextProviderSubscriptions(p memorySubscriptionDeps, appendCancel func(context.CancelFunc)) {
	if p.ContextProvider == nil {
		return
	}
	registerTurnTerminationSubscriptions(p, appendCancel)
}

// registerTurnTerminationSubscriptions 在 turn 完成、中断或 stalled 时清理上下文状态。
// 三类事件共享同一清理函数，保证预取和 surfaced 记录不会跨 turn 泄漏。
func registerTurnTerminationSubscriptions(p memorySubscriptionDeps, appendCancel func(context.CancelFunc)) {
	term := func(threadID, turnID string) { p.ContextProvider.onTurnTerminated(threadID, turnID) }
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnCompleted) { term(ev.ThreadID, ev.TurnID) }, pkglogger.Get()))
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnInterrupted) { term(ev.ThreadID, ev.TurnID) }, pkglogger.Get()))
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnStalled) { term(ev.ThreadID, ev.TurnID) }, pkglogger.Get()))
}

// cancelSubscriptions 逐个调用订阅取消函数。
// nil 取消函数会被跳过，方便构造阶段按功能开关条件追加订阅。
func cancelSubscriptions(cancels []context.CancelFunc) {
	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
}
