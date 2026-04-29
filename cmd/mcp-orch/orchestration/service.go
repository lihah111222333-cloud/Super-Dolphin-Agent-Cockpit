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
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/kelindar/event"
	"github.com/qmuntal/stateless"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformstatemachine "github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type Service = contract.OrchestrationService
type SessionCleaner = contract.OrchestrationSessionCleaner
type TurnStarter = contract.OrchestrationTurnStarter

type TurnSubmission = contract.TurnSubmission
type RuntimeReport = contract.RuntimeReport

type LaunchRequest = contract.LaunchRequest
type AgentSnapshot = contract.AgentSnapshot
type AgentStateResult = contract.AgentStateResult
type AgentReportMetadata = contract.AgentReportMetadata
type AgentReportResult = contract.AgentReportResult
type RememberReportRequest = contract.RememberReportRequest
type RememberReportRequestResult = contract.RememberReportRequestResult
type ReportEvent = contract.ReportEvent
type ReportEventResult = contract.ReportEventResult
type CreateDAGRequest = contract.CreateDAGRequest
type CreateDAGNodeRequest = contract.CreateDAGNodeRequest
type ListDAGsFilter = contract.ListDAGsFilter
type UpdateNodeStatusRequest = contract.UpdateNodeStatusRequest
type DAGSummary = contract.DAGSummary
type DAGNode = contract.DAGNode
type DAGDetail = contract.DAGDetail

// P22 P4 S4c1: orchestration no longer exports a package-level `Module`
// variable. Per P4 §278 the root entry (cmd/mcp-orch/fx.go
// buildOrchestrationOptions) now composes the orchestration wiring
// explicitly from the exported building blocks below — ProvideService /
// ProvideServiceInterface / ProvideHookAfterHandler / ProvideRPCFacade
// / RegisterTurnLifecycle / RegisterApprovalLifecycle. The archtest
// TestOrchestrationNoModuleExport locks this in place so the subpackage
// cannot re-grow a wholesale `Module` export.
//
// Keeping the building blocks exported rather than re-bundling them under
// a "helper" name keeps root assembly explicit and lets cmd/mcp-orch
// insert additional providers (noop cleaners, launcher variants,
// standalone stubs) without having to peel apart a pre-built bundle.

var (
	errAgentNotFound          = contract.ErrAgentNotFound
	errIllegalStateTransition = errors.New("illegal state transition")
	errTurnNotActive          = errors.New("turn is not active")
)

type service struct {
	logger                 *slog.Logger
	eventBus               *event.Dispatcher
	launcher               AgentLauncher
	sessionCleaner         SessionCleaner
	turnStarter            TurnStarter
	dagStore               taskdag.Store
	recoveryStore          recoveryTurnStore
	agentThreads           AgentThreadStore
	agentBindings          AgentBindingStore
	machineCfg             platformstatemachine.Config
	processExitWaitTimeout time.Duration
	// exitMonitor is the P22 P3 single owner of every locally-launched
	// agent process's cmd.Wait. runnerActor consumes its ExitEvents;
	// launcher-driven stops call Emit directly. See exit_monitor.go.
	exitMonitor *processExitMonitor
	mu          sync.RWMutex
	agents      map[string]*agentRuntime
	nextTurnSeq int64
}

type serviceParams struct {
	fx.In

	Logger         *slog.Logger
	EventBus       *event.Dispatcher
	Launcher       AgentLauncher
	SessionCleaner SessionCleaner
	TurnStarter    TurnStarter
	DAGStore       taskdag.Store     `optional:"true"`
	AgentThreads   AgentThreadStore  `optional:"true"`
	AgentBindings  AgentBindingStore `optional:"true"`
}

type recoveryTurnStore interface {
	ListRunningNodesByAssignee(ctx context.Context, assignee string) ([]taskdag.Node, error)
	GetWakeup(ctx context.Context, id int64) (*taskdag.Wakeup, error)
}

type agentRuntime struct {
	id                string
	name              string
	parentID          string
	cwd               string
	command           []string
	env               []string
	port              int
	runtimePort       int
	portSource        string
	provider          string
	runtimeProvider   string
	providerSource    string
	state             string
	threadID          string
	remoteThreadID    string
	remoteAgentID     string
	activeTurnID      string
	lastReport        string
	reportRequesters  []string
	lastError         string
	startedAt         time.Time
	updatedAt         time.Time
	exitedAt          *time.Time
	launchSeq         uint64
	lastExitedSeq     uint64
	monitoredSeq      uint64
	sessionGeneration uint64
	stopRequested     bool
	stopReason        string
	cmd               *exec.Cmd
	queue             *SubmissionQueue
	sm                *stateless.StateMachine
}

type monitorTarget struct {
	agentID   string
	launchSeq uint64
	cmd       *exec.Cmd
}

type turnWork struct {
	agentID    string
	threadID   string
	turnID     string
	submission TurnSubmission
}

func NewService(
	logger *slog.Logger,
	eventBus *event.Dispatcher,
	launcher AgentLauncher,
	sessionCleaner SessionCleaner,
	turnStarter TurnStarter,
	dagStore taskdag.Store,
) *service {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &service{
		logger:         logger,
		eventBus:       eventBus,
		launcher:       launcher,
		sessionCleaner: sessionCleaner,
		turnStarter:    turnStarter,
		dagStore:       dagStore,
		recoveryStore:  dagStore,
		machineCfg: platformstatemachine.Config{
			Initial: agentdto.StateProvisioning,
			States:  buildStatesFromDefinitions(agentdto.TransitionDefinitions),
		},
		processExitWaitTimeout: 30 * time.Second,
		exitMonitor:            newProcessExitMonitor(logger),
		agents:                 make(map[string]*agentRuntime),
	}
}

// ProvideService is the fx constructor for the orchestration service.
// Exported by P22 P4 S4c1 so cmd/mcp-orch/fx.go can assemble the
// orchestration wiring at root instead of consuming a package-level
// `Module`. Returns the private *service pointer so fx can resolve the
// concrete type for invokes that need it; ProvideServiceInterface
// adapts the same pointer to the public Service / contract.Orchestration
// Service interface.
func ProvideService(p serviceParams) *service {
	svc := NewService(p.Logger, p.EventBus, p.Launcher, p.SessionCleaner, p.TurnStarter, p.DAGStore)
	svc.agentThreads = p.AgentThreads
	svc.agentBindings = p.AgentBindings
	return svc
}

// ProvideServiceInterface adapts the private *service pointer into the
// public Service / contract.OrchestrationService interface. Previously
// this was an anonymous func inside `var Module`; exporting it keeps the
// adapter available to root-level fx assembly (P22 P4 S4c1).
func ProvideServiceInterface(s *service) Service { return s }

// RegisterTurnLifecycle was `registerTurnLifecycle` pre-P22 P4 S4c1.
// Exported so cmd/mcp-orch/fx.go can fx.Invoke it during root assembly.
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
			completedCancel = bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
				if lifecycleCtx.Err() != nil {
					return
				}
				handleTurnCompletedEventWithCtx(svc, logger, ev, lifecycleCtx)
			}, logger)
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
func RegisterApprovalLifecycle(lc fx.Lifecycle, dispatcher *event.Dispatcher, svc *service, logger *slog.Logger) {
	requestedCancel := func() {}
	resolvedCancel := func() {}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			requestedCancel = bus.ResilientSubscribe(dispatcher, func(ev tooldto.ToolApprovalRequested) {
				handleToolApprovalRequestedEvent(svc, loggerOrDefault(logger), ev)
			}, logger)
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

// RegisterWakeupDispatcherIn lets fx inject taskdag.Store as an optional
// dependency. Tests that do not bring up the taskdag module skip the
// dispatcher entirely; production wiring (cmd/mcp-orch/fx.go) always
// includes taskdagstore.Module so the dispatcher runs.
type RegisterWakeupDispatcherIn struct {
	fx.In

	Lifecycle fx.Lifecycle
	Store     taskdag.Store `optional:"true"`
	Service   *service
	Logger    *slog.Logger `optional:"true"`
}

// RegisterWakeupDispatcher (Phase 3.2) starts the wakeup dispatcher main
// loop on app start and stops it on app stop. The dispatcher claims due
// wakeups via taskdag.Store, hands each off to *service.LaunchAgent, and
// drives state transitions (MarkWakeupSent / RetryWakeup / FailWakeup).
//
// Wired with fx.Invoke from cmd/mcp-orch/fx.go so the orchestration module
// can compose it. taskdag.Store is optional: when missing the dispatcher
// is skipped (for tests that don't bring up the dag store).
func RegisterWakeupDispatcher(in RegisterWakeupDispatcherIn) error {
	logger := in.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	if in.Store == nil {
		logger.Info("orchestration: wakeup dispatcher disabled (no taskdag store provided)")
		return nil
	}
	dispatcher, err := NewWakeupDispatcher(in.Store, in.Service, logger, WakeupDispatcherConfig{})
	if err != nil {
		return err
	}
	var (
		cancel context.CancelFunc
		done   = make(chan struct{})
	)
	in.Lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			var runCtx context.Context
			runCtx, cancel = context.WithCancel(context.Background())
			go func() {
				defer close(done)
				if runErr := dispatcher.Run(runCtx); runErr != nil && !errors.Is(runErr, context.Canceled) {
					logger.Warn("orchestration: wakeup dispatcher exited",
						"claimed_by", dispatcher.ClaimedBy(),
						"error", runErr)
				}
			}()
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			if cancel != nil {
				cancel()
			}
			select {
			case <-done:
			case <-stopCtx.Done():
				logger.Warn("orchestration: wakeup dispatcher stop timeout",
					"claimed_by", dispatcher.ClaimedBy(),
					"error", stopCtx.Err())
			}
			return nil
		},
	})
	return nil
}

func withEventTime(ctx context.Context, timestamp time.Time) context.Context {
	return platformshared.WithEventTime(ctx, timestamp)
}

func resolveEventTime(ctx context.Context, fallbacks ...time.Time) time.Time {
	return platformshared.ResolveEventTime(ctx, nil, fallbacks...)
}

type runtimeReporter struct {
	svc Service
}

func (r runtimeReporter) ReportRuntime(ctx context.Context, report contract.RuntimeReport) error {
	return r.svc.UpdateRuntime(ctx, report)
}

func (s *service) LaunchAgent(ctx context.Context, req LaunchRequest) error {
	return s.launchAgentViaLauncher(ctx, req)
}

func (s *service) StopAgent(ctx context.Context, agentID string) error {
	return s.stopAgentViaLauncher(ctx, agentID, "user_requested")
}

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
}

func (s *service) SubmitTurn(ctx context.Context, req TurnSubmission) error {
	return s.submitTurnViaLauncher(ctx, req)
}

func (s *service) ListAgents(ctx context.Context) ([]AgentSnapshot, error) {
	snapshots := s.runtimeAgentSnapshots(ctx)
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

func (s *service) runtimeAgentSnapshots(ctx context.Context) []AgentSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshots := make([]AgentSnapshot, 0, len(s.agents))
	for _, agent := range s.agents {
		snapshots = append(snapshots, s.snapshotLocked(ctx, agent))
	}
	return snapshots
}

func (s *service) Snapshot(ctx context.Context, agentID string) (AgentSnapshot, error) {
	var snapshot AgentSnapshot
	err := s.withAgentReadLockedByAgentID(agentID, func(agent *agentRuntime) error {
		snapshot = s.snapshotLocked(ctx, agent)
		return nil
	})
	if err != nil && errors.Is(err, errAgentNotFound) {
		persisted, lookupErr := s.persistedAgentSnapshot(ctx, agentID)
		if lookupErr == nil {
			return persisted, nil
		}
		if !errors.Is(lookupErr, errAgentNotFound) {
			return AgentSnapshot{}, lookupErr
		}
	}
	return snapshot, err
}

func (s *service) GetAgentSnapshot(agentID string) (*AgentSnapshot, error) {
	snapshot, err := s.Snapshot(context.Background(), strings.TrimSpace(agentID))
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

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
