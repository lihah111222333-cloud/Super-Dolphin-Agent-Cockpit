// Package orchestration 实现 agent 生命周期管理、DAG 任务编排和 wakeup 投递。
// 核心结构是 service，负责 agent 状态机、turn 队列、进程守护和 DAG run 控制；
// 通过 AgentLauncher 抽象本地进程和远端 Codex 两种启动模式。
package orchestration

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/contextlock"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/exitmonitor"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/processctl"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/kelindar/event"
	"github.com/qmuntal/stateless"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	platformstatemachine "github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Service 是编排服务的导出类型别名，用于 fx 依赖注入。
type Service = contract.OrchestrationService

// SessionCleaner 是 session 清理器的导出类型别名。
type SessionCleaner = contract.OrchestrationSessionCleaner

// TurnStarter 是 turn 启动器的导出类型别名。
type TurnStarter = contract.OrchestrationTurnStarter

// TurnSubmission 是提交 turn 的跨模块 DTO 别名。
type TurnSubmission = contract.TurnSubmission

// RuntimeReport 是 runtime 上报端口/provider 的跨模块 DTO 别名。
type RuntimeReport = contract.RuntimeReport

// LaunchRequest 是启动 agent 的跨模块 DTO 别名。
type LaunchRequest = contract.LaunchRequest

// AgentSnapshot 是对外展示 agent 运行态的跨模块 DTO 别名。
type AgentSnapshot = contract.AgentSnapshot

// AgentStateResult 是查询 agent 状态的跨模块 DTO 别名。
type AgentStateResult = contract.AgentStateResult

// AgentReportMetadata 是 agent report 附加元数据的跨模块 DTO 别名。
type AgentReportMetadata = contract.AgentReportMetadata

// AgentReportResult 是查询 agent report 的跨模块 DTO 别名。
type AgentReportResult = contract.AgentReportResult

// RememberReportRequest 是登记 report requester 的跨模块 DTO 别名。
type RememberReportRequest = contract.RememberReportRequest

// RememberReportRequestResult 是登记 report requester 的返回 DTO 别名。
type RememberReportRequestResult = contract.RememberReportRequestResult

// ReportEvent 是 provider/hook report 事件的跨模块 DTO 别名。
type ReportEvent = contract.ReportEvent

// ReportEventResult 是 report 事件处理结果的跨模块 DTO 别名。
type ReportEventResult = contract.ReportEventResult

// CreateDAGRequest 是创建 DAG 的跨模块 DTO 别名。
type CreateDAGRequest = contract.CreateDAGRequest

// CreateDAGNodeRequest 是创建 DAG 节点的跨模块 DTO 别名。
type CreateDAGNodeRequest = contract.CreateDAGNodeRequest

// ListDAGsFilter 是 DAG 列表查询过滤条件的跨模块 DTO 别名。
type ListDAGsFilter = contract.ListDAGsFilter

// UpdateNodeStatusRequest 是更新 DAG 节点状态的跨模块 DTO 别名。
type UpdateNodeStatusRequest = contract.UpdateNodeStatusRequest

// DAGSummary 是 DAG 列表摘要的跨模块 DTO 别名。
type DAGSummary = contract.DAGSummary

// DAGNode 是 DAG 节点详情的跨模块 DTO 别名。
type DAGNode = contract.DAGNode

// DAGDetail 是 DAG 详情的跨模块 DTO 别名。
type DAGDetail = contract.DAGDetail

// ListRunsRequest 是 DAG run 列表查询请求的跨模块 DTO 别名。
type ListRunsRequest = contract.ListRunsRequest

// ListRunsResponse 是 DAG run 列表查询响应的跨模块 DTO 别名。
type ListRunsResponse = contract.ListRunsResponse

// Run 是 DAG runtime run 的跨模块 DTO 别名。
type Run = contract.Run

// service 内部错误哨兵。
var (
	errAgentNotFound          = contract.ErrAgentNotFound
	errIllegalStateTransition = errors.New("illegal state transition")
	errTurnNotActive          = errors.New("turn is not active")
)

// service 是 orchestration 包的核心实现结构体，持有 agent 状态机、turn 队列、
// DAG store 和异步后台任务上下文。所有并发访问通过 mu 保护。
type service struct {
	logger                   *slog.Logger
	eventBus                 *event.Dispatcher
	launcher                 AgentLauncher
	sessionCleaner           SessionCleaner
	turnStarter              TurnStarter
	dagStore                 taskdag.OrchestrationStore
	runStore                 taskdag.RunStore
	scheduledStartStore      taskdag.ScheduledStartStore
	dispatchStore            taskdag.DispatchNodeStore
	recoveryStore            recoveryTurnStore
	agentThreads             AgentThreadStore
	agentBindings            AgentBindingStore
	machineCfg               platformstatemachine.Config
	processExitWaitTimeout   time.Duration
	exitMonitor              *exitmonitor.Monitor
	mu                       contextlock.RWMutex
	agents                   map[string]*agentRuntime
	suppressedStoppedThreads sync.Map
	nextTurnSeq              int64

	asyncCtx    context.Context
	asyncCancel context.CancelFunc
	asyncWg     sync.WaitGroup
}

// serviceParams 是 fx 依赖注入参数结构体，包含 service 所需的所有依赖。
type serviceParams struct {
	fx.In

	Logger         *slog.Logger
	EventBus       *event.Dispatcher
	Launcher       AgentLauncher
	SessionCleaner SessionCleaner
	TurnStarter    TurnStarter
	DAGStore       taskdag.OrchestrationStore `optional:"true"`
	RunStore       taskdag.RunStore
	ScheduledStart taskdag.ScheduledStartStore
	AgentThreads   AgentThreadStore          `optional:"true"`
	AgentBindings  AgentBindingStore         `optional:"true"`
	DispatchStore  taskdag.DispatchNodeStore `optional:"true"`
}

// recoveryTurnStore 是 recoveryTurnStore 接口的本地类型别名，用于内部断言。
type recoveryTurnStore interface {
	taskdag.RecoveryStore
}

// agentRuntime 持有单个 agent 实例的完整运行时状态，由 service.mu 保护读写。
type agentRuntime struct {
	id, name, prompt, instructions, parentID, agentType, agentKey, memoryScope, language, cwd string
	command, env                                                                              []string
	port, runtimePort                                                                         int
	portSource, provider, runtimeProvider, providerSource                                     string
	state                                                                                     agentdto.AgentState
	threadID, remoteThreadID, pendingLaunchThreadID                                           string
	pendingLaunchThreadAt                                                                     time.Time
	remoteAgentID, requestedAgentID, activeTurnID, lastReport                                 string
	reportRequesters                                                                          []string
	lastError                                                                                 string
	lastReportSeq                                                                             int64
	startedAt, updatedAt, lastReportUpdatedAt                                                 time.Time
	exitedAt                                                                                  *time.Time
	launchSeq, lastExitedSeq, monitoredSeq, sessionGeneration                                 uint64
	autoRecoverCount                                                                          int
	autoRecoverSince                                                                          time.Time
	stopRequested                                                                             bool
	stopReason                                                                                string
	cmd                                                                                       *exec.Cmd
	processGuard                                                                              *processctl.Guard
	queue                                                                                     *SubmissionQueue
	sm                                                                                        *stateless.StateMachine
}

// turnWork 是 claimTurnWork 从队列取出的一次待执行 turn 的工作单元。
type turnWork struct {
	agentID    string
	threadID   string
	turnID     string
	submission TurnSubmission
}

// NewService 创建 orchestration service，并初始化状态机配置、恢复 store 和后台上下文。
func NewService(
	logger *slog.Logger,
	eventBus *event.Dispatcher,
	launcher AgentLauncher,
	sessionCleaner SessionCleaner,
	turnStarter TurnStarter,
	dagStore taskdag.OrchestrationStore,
) *service {
	if logger == nil {
		logger = pkglogger.Get()
	}
	var recoveryStore recoveryTurnStore
	if store, ok := dagStore.(recoveryTurnStore); ok {
		recoveryStore = store
	}
	asyncCtx, asyncCancel := context.WithCancel(context.Background())
	svc := &service{
		logger:         logger,
		eventBus:       eventBus,
		launcher:       launcher,
		sessionCleaner: sessionCleaner,
		turnStarter:    turnStarter,
		dagStore:       dagStore,
		recoveryStore:  recoveryStore,
		machineCfg: platformstatemachine.Config{
			Initial: string(agentdto.StateProvisioning),
			States:  buildStatesFromDefinitions(agentdto.TransitionDefinitions),
		},
		processExitWaitTimeout: 30 * time.Second,
		exitMonitor:            exitmonitor.New(logger),
		agents:                 make(map[string]*agentRuntime),
		asyncCtx:               asyncCtx,
		asyncCancel:            asyncCancel,
	}
	bindRemoteLauncherEventSink(launcher, svc)
	return svc
}

// ProvideService 从 fx 参数创建 service，并挂接可选 store 依赖。
func ProvideService(p serviceParams) *service {
	svc := NewService(p.Logger, p.EventBus, p.Launcher, p.SessionCleaner, p.TurnStarter, p.DAGStore)
	svc.runStore = p.RunStore
	svc.scheduledStartStore = p.ScheduledStart
	svc.agentThreads = p.AgentThreads
	svc.agentBindings = p.AgentBindings
	svc.dispatchStore = p.DispatchStore
	return svc
}

// ProvideServiceInterface 将具体 service 暴露为 contract.OrchestrationService。
func ProvideServiceInterface(s *service) Service { return s }

// RegisterTurnLifecycle 注册 turn started/completed/interrupted 事件订阅。
// 订阅在 fx OnStop 时取消，避免 service 停止后继续推进状态机。
func RegisterTurnLifecycle(lc fx.Lifecycle, dispatcher *event.Dispatcher, svc *service, logger *slog.Logger) {
	if logger == nil {
		logger = pkglogger.Get()
	}
	startedCancel := func() {}
	completedCancel := func() {}
	interruptedCancel := func() {}
	var (
		lifecycleCtx    context.Context
		lifecycleCancel context.CancelFunc
	)
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			lifecycleCtx, lifecycleCancel = context.WithCancel(context.Background())
			startedCancel = bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnStarted) {
				if lifecycleCtx.Err() != nil {
					return
				}
				ctx := withEventTime(lifecycleCtx, ev.Timestamp)
				if err := svc.BindActiveTurnID(ctx, ev.AgentID, ev.TurnID); err != nil && !errors.Is(err, errAgentNotFound) && !errors.Is(err, errTurnNotActive) {
					logger.Warn("orchestration: failed to bind active turn id", "agent_id", ev.AgentID, "turn_id", ev.TurnID, "error", err)
				}
			}, logger)
			// completion 事件会直接推进状态机；订阅随 lifecycleCtx 取消，避免服务停止后继续写状态。
			completedCancel = bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
				if lifecycleCtx.Err() != nil {
					return
				}
				handleTurnCompletedEventWithCtx(svc, logger, ev, lifecycleCtx)
			}, logger)
			// interruption 事件同样直接推进状态机；失败只记录，终态修复逻辑在 handler 内完成。
			interruptedCancel = bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnInterrupted) {
				if lifecycleCtx.Err() != nil {
					return
				}
				handleTurnInterruptedEventWithCtx(svc, logger, ev, lifecycleCtx)
			}, logger)
			return nil
		},
		OnStop: func(context.Context) error {
			if lifecycleCancel != nil {
				lifecycleCancel()
			}
			startedCancel()
			completedCancel()
			interruptedCancel()
			return nil
		},
	})
}

// RegisterApprovalLifecycle 注册 tool approval 事件订阅，用于驱动 awaiting_user_input 状态。
// 导出给 cmd/mcp-orch/fx.go 通过 fx.Invoke 接线。
func RegisterApprovalLifecycle(lc fx.Lifecycle, dispatcher *event.Dispatcher, svc *service, logger *slog.Logger) {
	requestedCancel := func() {}
	resolvedCancel := func() {}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			// approval requested 事件进入 awaiting_user_input；handler 内负责幂等处理漂移状态。
			requestedCancel = bus.ResilientSubscribe(dispatcher, func(ev tooldto.ToolApprovalRequested) {
				handleToolApprovalRequestedEvent(svc, loggerOrDefault(logger), ev)
			}, logger)
			// approval resolved 事件解除 awaiting_user_input；重复完成会被下游视为幂等。
			resolvedCancel = bus.ResilientSubscribe(dispatcher, func(ev tooldto.ToolApprovalResolved) {
				handleToolApprovalResolvedEvent(svc, loggerOrDefault(logger), ev)
			}, logger)
			return nil
		},
		OnStop: func(context.Context) error {
			requestedCancel()
			resolvedCancel()
			return nil
		},
	})
}

// loggerOrDefault 返回 logger，为 nil 时返回全局默认 logger。
func loggerOrDefault(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return pkglogger.Get()
}

// ProvideWakeupDispatcherRunnerIn 是 fx 注入 WakeupDispatcher runner 的参数结构。
// Store 允许为空，便于未装载 taskdag 模块的测试图跳过 dispatcher。
type ProvideWakeupDispatcherRunnerIn struct {
	fx.In

	Store   taskdag.Store `optional:"true"`
	Service *service
	Logger  *slog.Logger `optional:"true"`
}

// ProvideWakeupDispatcher 创建共享 wakeup dispatcher；未注入 Store 时返回 nil。
// runner 和 router wiring 复用同一个实例，避免生产图里出现多个 claim owner。
func ProvideWakeupDispatcher(in ProvideWakeupDispatcherRunnerIn) (*WakeupDispatcher, error) {
	logger := in.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	if in.Store == nil {
		logger.Info("orchestration: wakeup dispatcher disabled (no taskdag store provided)")
		return nil, nil
	}
	return NewWakeupDispatcher(in.Store, in.Service, logger, WakeupDispatcherConfig{})
}

// ProvideWakeupDispatcherRunner 将可选 dispatcher 适配为 runner。
// dispatcher 为 nil 时返回 no-op，保持轻量测试图不启动后台 claim 循环。
func ProvideWakeupDispatcherRunner(dispatcher *WakeupDispatcher) platformrunner.Runner {
	if dispatcher == nil {
		return platformrunner.NoopRunner{}
	}
	return dispatcher
}

// WireWakeupDispatcherRouter 是 fx.Invoke 的 dispatcher/router 接线点。
// router 为空表示当前图未提供 nodeexec 路由，dispatcher 保持非 DAG launcher 路径；
// dispatcher 为空表示测试或独立图跳过后台投递，此处只把 event bus 接到 router。
func WireWakeupDispatcherRouter(dispatcher *WakeupDispatcher, router *NodeExecutorRouter, bus *event.Dispatcher) {
	if router == nil {
		return
	}
	router.WithEventBus(bus)
	if dispatcher != nil {
		dispatcher.WithNodeRouter(router)
	}
}

// WireWakeupDispatcherRetryAlertSinkIn 是 fx 注入重试告警 sink 的参数结构。
type WireWakeupDispatcherRetryAlertSinkIn struct {
	fx.In

	Dispatcher *WakeupDispatcher      `optional:"true"`
	Sink       DispatchRetryAlertSink `optional:"true"`
}

// WireWakeupDispatcherRetryAlertSink 给 dispatcher 接入可选重试告警 sink。
func WireWakeupDispatcherRetryAlertSink(in WireWakeupDispatcherRetryAlertSinkIn) {
	if in.Dispatcher == nil {
		return
	}
	in.Dispatcher.WithDispatchRetryAlertSink(in.Sink)
}

// withEventTime 把事件时间戳注入 context，供后续状态更新时间源使用。
func withEventTime(ctx context.Context, timestamp time.Time) context.Context {
	return platformshared.WithEventTime(ctx, timestamp)
}

// resolveEventTime 从 context 或 fallback 中解析事件时间。
func resolveEventTime(ctx context.Context, fallbacks ...time.Time) time.Time {
	return platformshared.ResolveEventTime(ctx, nil, fallbacks...)
}

// LaunchAgent 启动 agent，当前统一走 launcher 桥接路径。
func (s *service) LaunchAgent(ctx context.Context, req LaunchRequest) error {
	return s.launchAgentViaLauncher(ctx, req)
}

// StopAgent 以 user_requested 原因停止 agent。
func (s *service) StopAgent(ctx context.Context, agentID string) error {
	return s.stopAgentViaLauncher(ctx, agentID, "user_requested")
}

// StopAllAgents 按字母顺序停止所有运行中的 agent，并等待异步任务完成。
func (s *service) StopAllAgents() {
	s.mu.RLock()
	ids := make([]string, 0, len(s.agents))
	for agentID := range s.agents {
		ids = append(ids, agentID)
	}
	s.mu.RUnlock()
	sort.Strings(ids)
	for _, agentID := range ids {
		if err := s.stopAgentViaLauncher(context.Background(), agentID, "shutdown"); err != nil &&
			!errors.Is(err, errAgentNotFound) {
			s.logger.Warn("orchestration: failed to stop agent during shutdown", "agent_id", agentID, "error", err)
		}
	}
	s.DrainAsync()
}

// DrainAsync 取消 service 共享异步 context，并等待已登记 goroutine 收尾。
// 多次调用是安全的：cancel 本身幂等，asyncWg 归零后后续调用会立即返回。
func (s *service) DrainAsync() {
	if s.asyncCancel != nil {
		s.asyncCancel()
	}
	s.asyncWg.Wait()
}

// SubmitTurn 提交一次 turn，远端 agent 优先走 launcher，其他 agent 进入本地队列。
func (s *service) SubmitTurn(ctx context.Context, req TurnSubmission) error {
	return s.submitTurnViaLauncher(ctx, req)
}

// ListAgents 合并内存 runtime 和持久化 thread 快照后排序返回。
func (s *service) ListAgents(ctx context.Context) ([]AgentSnapshot, error) {
	snapshots, err := s.runtimeAgentSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	if s.agentThreads != nil {
		persisted, err := s.listPersistedAgentSnapshots(ctx)
		if err != nil {
			return nil, err
		}
		snapshots = mergeAgentSnapshots(persisted, snapshots)
	}
	sortAgentSnapshots(snapshots)
	return snapshots, nil
}

// Snapshot 返回单个 agent 快照；runtime 不存在时回退到持久化 thread/binding。
func (s *service) Snapshot(ctx context.Context, agentID string) (AgentSnapshot, error) {
	var snapshot AgentSnapshot
	err := s.withAgentReadLockedByAgentID(ctx, agentID, func(agent *agentRuntime) error {
		snapshot = s.snapshotLocked(ctx, agent)
		return nil
	})
	if errors.Is(err, errAgentNotFound) {
		return s.persistedAgentSnapshot(ctx, agentID)
	}
	return snapshot, err
}

// GetAgentSnapshot 是 provider 侧使用的快照读取适配器。
func (s *service) GetAgentSnapshot(agentID string) (*AgentSnapshot, error) {
	snapshot, err := s.Snapshot(context.Background(), strings.TrimSpace(agentID))
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// CompleteTurn 根据 provider 终态事件完成或中止当前 active turn。
func (s *service) CompleteTurn(ctx context.Context, agentID, turnID string, success bool, errMsg string) error {
	return s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		kind := activeTurnFinalizationKind{
			trigger:   agentdto.TriggerTurnAborted,
			errorText: errMsg,
		}
		if success {
			kind.trigger = agentdto.TriggerTurnCompleted
			kind.clearError = true
		}
		return s.finalizeActiveTurnLocked(ctx, agent, turnID, kind)
	})
}
