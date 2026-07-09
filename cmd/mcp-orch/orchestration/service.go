// Package orchestration 实现 agent 生命周期管理、DAG 任务编排和 wakeup 投递。
// service 只保留 RPC/fx 门面和控制器委派；agentRegistry、lifecycleController、
// dagController、turnController 和 reportController 分别拥有运行态、生命周期、DAG、
// turn 与报告职责。AgentLauncher 抽象本地进程和远端 Codex 两种启动模式。
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

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/exitmonitor"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/processctl"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
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

// RPC 请求/结果类型是 contract 层 orchestration RPC 契约在本包的复导出。
// 这些别名只固定 service 与 transport 的 DTO 边界名称，状态转换和序列化语义仍以 contract 包为准。
type (
	TurnSubmission              = contract.TurnSubmission
	RuntimeReport               = contract.RuntimeReport
	LaunchRequest               = contract.LaunchRequest
	AgentSnapshot               = contract.AgentSnapshot
	AgentStateResult            = contract.AgentStateResult
	AgentReportMetadata         = contract.AgentReportMetadata
	AgentReportResult           = contract.AgentReportResult
	RememberReportRequest       = contract.RememberReportRequest
	RememberReportRequestResult = contract.RememberReportRequestResult
	ReportEvent                 = contract.ReportEvent
	ReportEventResult           = contract.ReportEventResult
	CreateDAGRequest            = contract.CreateDAGRequest
	CreateDAGNodeRequest        = contract.CreateDAGNodeRequest
	ListDAGsFilter              = contract.ListDAGsFilter
	UpdateNodeStatusRequest     = contract.UpdateNodeStatusRequest
	DAGSummary                  = contract.DAGSummary
	DAGNode                     = contract.DAGNode
	DAGDetail                   = contract.DAGDetail
)

var (
	errAgentNotFound             = contract.ErrAgentNotFound
	errIllegalStateTransition    = errors.New("illegal state transition")
	errTurnNotActive             = errors.New("turn is not active")
	errAgentNotRunningForStopper = errors.New("agent is not running")
	errAgentStoppingForStopper   = errors.New("agent is stopping")
)

type service struct {
	logger    *slog.Logger
	eventBus  *event.Dispatcher
	registry  *agentRegistry
	lifecycle *lifecycleController
	dags      *dagController
	turns     *turnController
	reports   *reportController
}

// lifecycleController owns agent launch/stop/process-exit state and service-scoped async bookkeeping.
type lifecycleController struct {
	launcher               AgentLauncher
	sessionCleaner         contract.OrchestrationSessionCleaner
	recoveryStore          recoveryTurnStore
	agentThreads           AgentThreadStore
	agentBindings          AgentBindingStore
	machineCfg             platformstatemachine.Config
	processExitWaitTimeout time.Duration
	exitMonitor            *exitmonitor.Monitor
	asyncCtx               context.Context
	asyncCancel            context.CancelFunc
	asyncWg                sync.WaitGroup
}

type lifecycleControllerParams struct {
	logger         *slog.Logger
	launcher       AgentLauncher
	sessionCleaner contract.OrchestrationSessionCleaner
	dagStore       taskdag.OrchestrationStore
}

func newLifecycleController(p lifecycleControllerParams) *lifecycleController {
	var recoveryStore recoveryTurnStore
	if store, ok := p.dagStore.(recoveryTurnStore); ok {
		recoveryStore = store
	}
	asyncCtx, asyncCancel := context.WithCancel(context.Background())
	return &lifecycleController{
		launcher:       p.launcher,
		sessionCleaner: p.sessionCleaner,
		recoveryStore:  recoveryStore,
		machineCfg: platformstatemachine.Config{
			Initial: string(agentdto.StateProvisioning),
			States:  buildStatesFromDefinitions(agentdto.TransitionDefinitions),
		},
		processExitWaitTimeout: 30 * time.Second,
		exitMonitor:            exitmonitor.New(p.logger),
		asyncCtx:               asyncCtx,
		asyncCancel:            asyncCancel,
	}
}

type turnStatePort interface {
	fireOrForceLocked(ctx context.Context, agent *agentRuntime, trigger agentdto.AgentTrigger) error
	finalizeActiveTurnLocked(ctx context.Context, agent *agentRuntime, turnID string, kind activeTurnFinalizationKind) error
	ensureTurnStartedLocked(ctx context.Context, agent *agentRuntime, trigger agentdto.AgentTrigger, states ...agentdto.AgentState) error
}

type turnRuntimeRehydrator interface {
	ensureRuntimeForPersistedAgent(ctx context.Context, agentID string)
}

type turnStopPort interface {
	stopAgentViaLauncher(ctx context.Context, agentID, reason string) error
}

type turnControllerDeps struct {
	registry    *agentRegistry
	launcher    AgentLauncher
	turnStarter contract.OrchestrationTurnStarter
	state       turnStatePort
	rehydrator  turnRuntimeRehydrator
	stopper     turnStopPort
	logger      *slog.Logger
}

// turnController owns turn submission and active-turn state transitions.
type turnController struct {
	registry    *agentRegistry
	launcher    AgentLauncher
	turnStarter contract.OrchestrationTurnStarter
	state       turnStatePort
	rehydrator  turnRuntimeRehydrator
	stopper     turnStopPort
	logger      *slog.Logger
}

func newTurnController(deps turnControllerDeps) *turnController {
	return &turnController{
		registry:    deps.registry,
		launcher:    deps.launcher,
		turnStarter: deps.turnStarter,
		state:       deps.state,
		rehydrator:  deps.rehydrator,
		stopper:     deps.stopper,
		logger:      deps.logger,
	}
}

func (c *turnController) agentRunningLocked(ctx context.Context, agent *agentRuntime) bool {
	if agent == nil {
		return false
	}
	if c.launcher != nil {
		return c.launcher.IsRunning(ctx, agent)
	}
	return agent.cmd != nil
}

func (c *turnController) withAgentLocked(agentID string, fn func(*agentRuntime) error) error {
	if c == nil || c.registry == nil {
		return errAgentNotFound
	}
	return c.registry.withAgentLocked(agentID, fn)
}

func (c *turnController) withAgentReadLocked(agentID string, fn func(*agentRuntime) error) error {
	if c == nil || c.registry == nil {
		return errAgentNotFound
	}
	return c.registry.withAgentReadLocked(agentID, fn)
}

func (c *turnController) turnIDFor(sub TurnSubmission) string {
	if c != nil && c.registry != nil {
		return c.registry.turnIDFor(sub)
	}
	return strings.TrimSpace(sub.ExpectedTurnID)
}

func (c *turnController) fireOrForceLocked(ctx context.Context, agent *agentRuntime, trigger agentdto.AgentTrigger) error {
	if c == nil || c.state == nil {
		return errors.New("turn state port is not configured")
	}
	return c.state.fireOrForceLocked(ctx, agent, trigger)
}

func (c *turnController) finalizeActiveTurnLocked(ctx context.Context, agent *agentRuntime, turnID string, kind activeTurnFinalizationKind) error {
	if c == nil || c.state == nil {
		return errors.New("turn state port is not configured")
	}
	return c.state.finalizeActiveTurnLocked(ctx, agent, turnID, kind)
}

func (c *turnController) ensureTurnStartedLocked(ctx context.Context, agent *agentRuntime, trigger agentdto.AgentTrigger, states ...agentdto.AgentState) error {
	if c == nil || c.state == nil {
		return errors.New("turn state port is not configured")
	}
	return c.state.ensureTurnStartedLocked(ctx, agent, trigger, states...)
}

func (c *turnController) log() *slog.Logger {
	if c != nil && c.logger != nil {
		return c.logger
	}
	return pkglogger.Get()
}

// serviceParams 是 fx 依赖注入参数结构体，包含 service 所需的所有依赖。
type serviceParams struct {
	fx.In

	Logger         *slog.Logger
	EventBus       *event.Dispatcher
	Launcher       AgentLauncher
	SessionCleaner contract.OrchestrationSessionCleaner
	TurnStarter    contract.OrchestrationTurnStarter
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

// agentRuntime 持有单个 agent 实例的完整运行时状态，由 agentRegistry.mu 保护读写。
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
	sessionCleaner contract.OrchestrationSessionCleaner,
	turnStarter contract.OrchestrationTurnStarter,
	dagStore taskdag.OrchestrationStore,
) *service {
	if logger == nil {
		logger = pkglogger.Get()
	}
	registry := newAgentRegistry()
	lifecycle := newLifecycleController(lifecycleControllerParams{
		logger:         logger,
		launcher:       launcher,
		sessionCleaner: sessionCleaner,
		dagStore:       dagStore,
	})
	svc := &service{
		logger:    logger,
		eventBus:  eventBus,
		registry:  registry,
		lifecycle: lifecycle,
	}
	svc.dags = newDAGController(dagControllerParams{
		Logger:     logger,
		EventBus:   eventBus,
		DAGStore:   dagStore,
		SvcStopper: svc,
	})
	svc.turns = newTurnController(turnControllerDeps{
		registry:    registry,
		launcher:    lifecycle.launcher,
		turnStarter: turnStarter,
		state:       svc,
		rehydrator:  svc,
		stopper:     svc,
		logger:      logger,
	})
	svc.reports = newReportController(reportControllerDeps{
		registry: registry,
		logger:   logger,
	})
	bindRemoteLauncherEventSink(launcher, svc)
	if local, ok := launcher.(*localLauncher); ok {
		local.exitMonitor = lifecycle.exitMonitor
	}
	return svc
}

// ProvideService 从 fx 参数创建 service，并挂接可选 store 依赖。
func ProvideService(p serviceParams) *service {
	svc := NewService(p.Logger, p.EventBus, p.Launcher, p.SessionCleaner, p.TurnStarter, p.DAGStore)
	svc.lifecycle.agentThreads = p.AgentThreads
	svc.lifecycle.agentBindings = p.AgentBindings
	svc.reports.agentThreads = p.AgentThreads
	svc.dags = newDAGController(dagControllerParams{
		Logger:              svc.logger,
		EventBus:            svc.eventBus,
		DAGStore:            p.DAGStore,
		RunStore:            p.RunStore,
		ScheduledStartStore: p.ScheduledStart,
		DispatchStore:       p.DispatchStore,
		AgentThreads:        p.AgentThreads,
		SvcStopper:          svc,
	})
	return svc
}

// RegisterTurnLifecycle 注册 turn started/completed/interrupted 事件订阅。
// 订阅在 fx OnStop 时取消，避免 service 停止后继续推进状态机。
func RegisterTurnLifecycle(lc fx.Lifecycle, dispatcher *event.Dispatcher, runtime TurnLifecyclePort, logger *slog.Logger) {
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
				if err := runtime.BindActiveTurnID(ctx, ev.AgentID, ev.TurnID); err != nil && !errors.Is(err, errAgentNotFound) && !errors.Is(err, errTurnNotActive) {
					logger.Warn("orchestration: failed to bind active turn id", "agent_id", ev.AgentID, "turn_id", ev.TurnID, "error", err)
				}
			}, logger)
			// completion 事件会直接推进状态机；订阅随 lifecycleCtx 取消，避免服务停止后继续写状态。
			completedCancel = bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
				if lifecycleCtx.Err() != nil {
					return
				}
				handleTurnCompletedEventWithCtx(runtime, logger, ev, lifecycleCtx)
			}, logger)
			// interruption 事件同样直接推进状态机；失败只记录，终态修复逻辑在 handler 内完成。
			interruptedCancel = bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnInterrupted) {
				if lifecycleCtx.Err() != nil {
					return
				}
				handleTurnInterruptedEventWithCtx(runtime, logger, ev, lifecycleCtx)
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
func RegisterApprovalLifecycle(lc fx.Lifecycle, dispatcher *event.Dispatcher, runtime ApprovalLifecyclePort, logger *slog.Logger) {
	requestedCancel := func() {}
	resolvedCancel := func() {}
	lifecycleCtx := context.Background()
	lifecycleCancel := func() {}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			lifecycleCtx, lifecycleCancel = context.WithCancel(context.Background())
			// approval requested 事件进入 awaiting_user_input；handler 内负责幂等处理漂移状态。
			requestedCancel = bus.ResilientSubscribe(dispatcher, func(ev tooldto.ToolApprovalRequested) {
				if lifecycleCtx.Err() != nil {
					return
				}
				handleToolApprovalRequestedEvent(runtime, loggerOrDefault(logger), ev)
			}, logger)
			// approval resolved 事件解除 awaiting_user_input；重复完成会被下游视为幂等。
			resolvedCancel = bus.ResilientSubscribe(dispatcher, func(ev tooldto.ToolApprovalResolved) {
				if lifecycleCtx.Err() != nil {
					return
				}
				handleToolApprovalResolvedEvent(runtime, loggerOrDefault(logger), ev)
			}, logger)
			return nil
		},
		OnStop: func(context.Context) error {
			lifecycleCancel()
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

	Store    taskdag.Store `optional:"true"`
	Launcher WakeupLauncher
	Logger   *slog.Logger `optional:"true"`
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
	return NewWakeupDispatcher(in.Store, in.Launcher, logger, WakeupDispatcherConfig{})
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

// StopAllAgents 按字母顺序停止所有运行中的 agent，并在调用方 deadline 内等待异步任务完成。
func (s *service) StopAllAgents(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	ids := s.agentRegistry().agentIDs()
	sort.Strings(ids)
	for _, agentID := range ids {
		if err := s.stopAgentViaLauncher(ctx, agentID, "shutdown"); err != nil &&
			!errors.Is(err, errAgentNotFound) {
			s.logger.Warn("orchestration: failed to stop agent during shutdown", "agent_id", agentID, "error", err)
		}
	}
	s.DrainAsync(ctx)
}

// DrainAsync 取消 service 共享异步 context，并等待已登记 goroutine 收尾。
// 多次调用是安全的：cancel 本身幂等，asyncWg 归零后后续调用会立即返回。
func (s *service) DrainAsync(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.lifecycle != nil && s.lifecycle.asyncCancel != nil {
		s.lifecycle.asyncCancel()
	}
	done := make(chan struct{})
	runtimesafe.SafeGo(ctx, s.logger, "orchestration.drainAsync", func(context.Context) {
		defer close(done)
		if s.lifecycle != nil {
			s.lifecycle.asyncWg.Wait()
		}
	})
	select {
	case <-done:
	case <-ctx.Done():
		s.logger.Warn("orchestration: async drain deadline reached", "error", ctx.Err())
	}
}

// SubmitTurn 提交一次 turn，远端 agent 优先走 launcher，其他 agent 进入本地队列。
func (s *service) SubmitTurn(ctx context.Context, req TurnSubmission) error {
	if s == nil || s.turns == nil {
		return errors.New("turn controller is not configured")
	}
	return s.turns.SubmitTurn(ctx, req)
}

// ListAgents 合并内存 runtime 和持久化 thread 快照后排序返回。
func (s *service) ListAgents(ctx context.Context) ([]AgentSnapshot, error) {
	snapshots, err := s.runtimeAgentSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	if s.lifecycle != nil && s.lifecycle.agentThreads != nil {
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
	if s == nil || s.turns == nil {
		return errors.New("turn controller is not configured")
	}
	return s.turns.CompleteTurn(ctx, agentID, turnID, success, errMsg)
}
