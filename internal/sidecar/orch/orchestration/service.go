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

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/orchestration/contextlock"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/orchestration/exitmonitor"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/orchestration/processctl"
	"github.com/kelindar/event"
	"github.com/qmuntal/stateless"
	"go.uber.org/fx"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	platformstatemachine "github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/taskdag"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Service provides orchestration use-case operations.
type Service = contract.OrchestrationService

// SessionCleaner describes a orchestration API type.
type SessionCleaner = contract.OrchestrationSessionCleaner

// TurnStarter describes a orchestration API type.
type TurnStarter = contract.OrchestrationTurnStarter

// TurnSubmission describes a orchestration API type.
type TurnSubmission = contract.TurnSubmission

// RuntimeReport describes a orchestration API type.
type RuntimeReport = contract.RuntimeReport

// LaunchRequest carries input for orchestration operations.
type LaunchRequest = contract.LaunchRequest

// AgentSnapshot describes a orchestration API type.
type AgentSnapshot = contract.AgentSnapshot

// AgentStateResult contains output returned by orchestration operations.
type AgentStateResult = contract.AgentStateResult

// AgentReportMetadata describes a orchestration API type.
type AgentReportMetadata = contract.AgentReportMetadata

// AgentReportResult contains output returned by orchestration operations.
type AgentReportResult = contract.AgentReportResult

// RememberReportRequest carries input for orchestration operations.
type RememberReportRequest = contract.RememberReportRequest

// RememberReportRequestResult contains output returned by orchestration operations.
type RememberReportRequestResult = contract.RememberReportRequestResult

// ReportEvent describes orchestration integration data.
type ReportEvent = contract.ReportEvent

// ReportEventResult contains output returned by orchestration operations.
type ReportEventResult = contract.ReportEventResult

// CreateDAGRequest carries input for orchestration operations.
type CreateDAGRequest = contract.CreateDAGRequest

// CreateDAGNodeRequest carries input for orchestration operations.
type CreateDAGNodeRequest = contract.CreateDAGNodeRequest

// ListDAGsFilter carries input for orchestration operations.
type ListDAGsFilter = contract.ListDAGsFilter

// UpdateNodeStatusRequest carries input for orchestration operations.
type UpdateNodeStatusRequest = contract.UpdateNodeStatusRequest

// DAGSummary describes a orchestration API type.
type DAGSummary = contract.DAGSummary

// DAGNode describes a orchestration API type.
type DAGNode = contract.DAGNode

// DAGDetail describes a orchestration API type.
type DAGDetail = contract.DAGDetail

// ListRunsRequest carries input for orchestration operations.
type ListRunsRequest = contract.ListRunsRequest

// ListRunsResponse contains output returned by orchestration operations.
type ListRunsResponse = contract.ListRunsResponse

// Run describes a orchestration API type.
type Run = contract.Run

var (
	errAgentNotFound          = contract.ErrAgentNotFound
	errIllegalStateTransition = errors.New("illegal state transition")
	errTurnNotActive          = errors.New("turn is not active")
)

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

type recoveryTurnStore interface {
	taskdag.RecoveryStore
}

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
	startedAt, updatedAt                                                                      time.Time
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

type turnWork struct {
	agentID    string
	threadID   string
	turnID     string
	submission TurnSubmission
}

// NewService 创建服务。
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

// ProvideService 提供服务。
func ProvideService(p serviceParams) *service {
	svc := NewService(p.Logger, p.EventBus, p.Launcher, p.SessionCleaner, p.TurnStarter, p.DAGStore)
	svc.runStore = p.RunStore
	svc.scheduledStartStore = p.ScheduledStart
	svc.agentThreads = p.AgentThreads
	svc.agentBindings = p.AgentBindings
	svc.dispatchStore = p.DispatchStore
	return svc
}

// ProvideServiceInterface 提供服务interface。
func ProvideServiceInterface(s *service) Service { return s }

// RegisterTurnLifecycle 注册turn生命周期。
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
			// NOTE(convention): event handler 直接操作状态机违反 statemachine-event-convention B7。
			// 当前安全（kelindar/event 异步投递），但应改为 trigger channel 解耦。
			completedCancel = bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
				if lifecycleCtx.Err() != nil {
					return
				}
				handleTurnCompletedEventWithCtx(svc, logger, ev, lifecycleCtx)
			}, logger)
			// NOTE(convention): event handler 直接操作状态机违反 statemachine-event-convention B7。
			// 当前安全（kelindar/event 异步投递），但应改为 trigger channel 解耦。
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

// RegisterApprovalLifecycle was `registerApprovalLifecycle` pre-P22 P4
// S4c1. Exported so cmd/mcp-orch/fx.go can fx.Invoke it during root
// assembly.
// RegisterApprovalLifecycle 注册审批生命周期。
func RegisterApprovalLifecycle(lc fx.Lifecycle, dispatcher *event.Dispatcher, svc *service, logger *slog.Logger) {
	requestedCancel := func() {}
	resolvedCancel := func() {}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			// NOTE(convention): event handler 直接操作状态机违反 statemachine-event-convention B7。
			// 当前安全（kelindar/event 异步投递），但应改为 trigger channel 解耦。
			requestedCancel = bus.ResilientSubscribe(dispatcher, func(ev tooldto.ToolApprovalRequested) {
				handleToolApprovalRequestedEvent(svc, loggerOrDefault(logger), ev)
			}, logger)
			// NOTE(convention): event handler 直接操作状态机违反 statemachine-event-convention B7。
			// 当前安全（kelindar/event 异步投递），但应改为 trigger channel 解耦。
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

func loggerOrDefault(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return pkglogger.Get()
}

// ProvideWakeupDispatcherRunnerIn lets fx inject taskdag.Store as an optional
// dependency. Tests that do not bring up the taskdag module skip the
// dispatcher entirely; production wiring (cmd/mcp-orch/fx.go) always
// includes taskdagstore.Module so the dispatcher runs.
type ProvideWakeupDispatcherRunnerIn struct {
	fx.In

	Store   taskdag.Store `optional:"true"`
	Service *service
	Logger  *slog.Logger `optional:"true"`
}

// ProvideWakeupDispatcher creates the shared dispatcher used by the runner
// adapter and router wiring; nil Store disables it for standalone tests.
// ProvideWakeupDispatcher 提供wakeup调度器。
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

// ProvideWakeupDispatcherRunner adapts the optional shared wakeup dispatcher
// into a run.Group runner, falling back to a no-op when the store is absent.
// ProvideWakeupDispatcherRunner 提供wakeup调度器runner。
func ProvideWakeupDispatcherRunner(dispatcher *WakeupDispatcher) platformrunner.Runner {
	if dispatcher == nil {
		return platformrunner.NoopRunner{}
	}
	return dispatcher
}

// WireWakeupDispatcherRouter 是 dispatcher-wiring batch §1 的接线点：fx.Invoke
// 拿 *WakeupDispatcher + NodeExecutorRouter (两者都可为 nil) 后调 setter 装上
// router。任一为 nil 都是合法路径：
//   - dispatcher nil (standalone)         → no-op
//   - router nil     (未提供 nodeexec providers) → dispatcher 退化到 legacy launcher
//
// WireWakeupDispatcherRouter is the fx.Invoke entry: with both *WakeupDispatcher
// and *NodeExecutorRouter resolved (either may be nil), call WithNodeRouter so
// dispatcher gains DAG-aware node_type routing in production wiring.
func WireWakeupDispatcherRouter(dispatcher *WakeupDispatcher, router *NodeExecutorRouter, bus *event.Dispatcher) {
	if router == nil {
		return
	}
	router.WithEventBus(bus)
	if dispatcher != nil {
		dispatcher.WithNodeRouter(router)
	}
}

// WireWakeupDispatcherRetryAlertSinkIn describes a orchestration API type.
type WireWakeupDispatcherRetryAlertSinkIn struct {
	fx.In

	Dispatcher *WakeupDispatcher      `optional:"true"`
	Sink       DispatchRetryAlertSink `optional:"true"`
}

// WireWakeupDispatcherRetryAlertSink 处理wirewakeup调度器重试alertsink。
func WireWakeupDispatcherRetryAlertSink(in WireWakeupDispatcherRetryAlertSinkIn) {
	if in.Dispatcher == nil {
		return
	}
	in.Dispatcher.WithDispatchRetryAlertSink(in.Sink)
}

func withEventTime(ctx context.Context, timestamp time.Time) context.Context {
	return platformshared.WithEventTime(ctx, timestamp)
}

func resolveEventTime(ctx context.Context, fallbacks ...time.Time) time.Time {
	return platformshared.ResolveEventTime(ctx, nil, fallbacks...)
}

// LaunchAgent 启动代理。
func (s *service) LaunchAgent(ctx context.Context, req LaunchRequest) error {
	return s.launchAgentViaLauncher(ctx, req)
}

// StopAgent 停止代理。
func (s *service) StopAgent(ctx context.Context, agentID string) error {
	return s.stopAgentViaLauncher(ctx, agentID, "user_requested")
}

// StopAllAgents 停止all代理。
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

// DrainAsync cancels the shared async context and waits for all
// fire-and-forget goroutines tracked by asyncWg to finish. Safe to
// call multiple times; the second+ invocations are no-ops because
// asyncCancel is idempotent and asyncWg is already at zero.
// DrainAsync 异步等待服务收尾。
func (s *service) DrainAsync() {
	if s.asyncCancel != nil {
		s.asyncCancel()
	}
	s.asyncWg.Wait()
}

// SubmitTurn 提交turn。
func (s *service) SubmitTurn(ctx context.Context, req TurnSubmission) error {
	return s.submitTurnViaLauncher(ctx, req)
}

// ListAgents 列出代理。
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

// Snapshot 处理快照。
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

// GetAgentSnapshot 读取代理快照。
func (s *service) GetAgentSnapshot(agentID string) (*AgentSnapshot, error) {
	snapshot, err := s.Snapshot(context.Background(), strings.TrimSpace(agentID))
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// CompleteTurn 完成turn。
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
