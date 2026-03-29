package orchestration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/kelindar/event"
	"github.com/qmuntal/stateless"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformstatemachine "github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
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

var Module = fx.Module("orchestration",
	fx.Provide(
		NewService,
		func(s *service) Service { return s },
		func(s Service) contract.RuntimeReporter { return runtimeReporter{svc: s} },
		NewOrchestrationHandlers,
		fx.Annotate(NewRunnerActor, fx.ResultTags(`group:"runners"`)),
	),
	fx.Invoke(registerTurnLifecycle),
	fx.Invoke(registerApprovalLifecycle),
)

var (
	errAgentNotFound          = contract.ErrAgentNotFound
	errIllegalStateTransition = errors.New("illegal state transition")
	errTurnNotActive          = errors.New("turn is not active")
)

type service struct {
	logger                 *slog.Logger
	eventBus               *event.Dispatcher
	sessionCleaner         SessionCleaner
	turnStarter            TurnStarter
	dagStore               taskdag.Store
	recoveryStore          recoveryTurnStore
	machineCfg             platformstatemachine.Config
	processExitWaitTimeout time.Duration
	mu                     sync.RWMutex
	agents                 map[string]*agentRuntime
	nextTurnSeq            int64
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
		sessionCleaner: sessionCleaner,
		turnStarter:    turnStarter,
		dagStore:       dagStore,
		recoveryStore:  dagStore,
		machineCfg: platformstatemachine.Config{
			Initial: agentdto.StateProvisioning,
			States:  buildStatesFromDefinitions(agentdto.TransitionDefinitions),
		},
		processExitWaitTimeout: 30 * time.Second,
		agents:                 make(map[string]*agentRuntime),
	}
}

func registerTurnLifecycle(lc fx.Lifecycle, dispatcher *event.Dispatcher, svc *service, logger *slog.Logger) {
	if logger == nil {
		logger = pkglogger.Get()
	}
	startedCancel := func() {}
	completedCancel := func() {}
	interruptedCancel := func() {}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			startedCancel = bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnStarted) {
				ctx := withEventTime(context.Background(), ev.Timestamp)
				if err := svc.BindActiveTurnID(ctx, ev.AgentID, ev.TurnID); err != nil && !errors.Is(err, errAgentNotFound) && !errors.Is(err, errTurnNotActive) {
					logger.Warn("orchestration: failed to bind active turn id", "agent_id", ev.AgentID, "turn_id", ev.TurnID, "error", err)
				}
			}, logger)
			completedCancel = bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
				handleTurnCompletedEvent(svc, logger, ev)
			}, logger)
			interruptedCancel = bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnInterrupted) {
				handleTurnInterruptedEvent(svc, logger, ev)
			}, logger)
			return nil
		},
		OnStop: func(context.Context) error {
			startedCancel()
			completedCancel()
			interruptedCancel()
			return nil
		},
	})
}

func registerApprovalLifecycle(lc fx.Lifecycle, dispatcher *event.Dispatcher, svc *service, logger *slog.Logger) {
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

func withEventTime(ctx context.Context, timestamp time.Time) context.Context {
	return sharedto.WithEventTime(ctx, timestamp)
}

func resolveEventTime(ctx context.Context, fallbacks ...time.Time) time.Time {
	return sharedto.ResolveEventTime(ctx, nil, fallbacks...)
}

type runtimeReporter struct {
	svc Service
}

func (r runtimeReporter) ReportRuntime(ctx context.Context, report contract.RuntimeReport) error {
	return r.svc.UpdateRuntime(ctx, report)
}

func (s *service) LaunchAgent(ctx context.Context, req LaunchRequest) error {
	if err := validateLaunchRequest(req); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	agent := s.agentForLaunchLocked(req)
	if agent.cmd != nil {
		return fmt.Errorf("agent %q already launched", agent.id)
	}
	agent.queue.Clear()
	if err := s.prepareLaunchStateLocked(ctx, agent); err != nil {
		return err
	}
	return s.startProcessLocked(ctx, agent)
}

func (s *service) StopAgent(ctx context.Context, agentID string) error {
	return s.stopAgentWithReason(ctx, agentID, "user_requested")
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
		if err := s.stopAgentWithReason(context.Background(), agentID, "shutdown"); err != nil &&
			!errors.Is(err, errAgentNotFound) {
			s.logger.Warn("orchestration: failed to stop agent during shutdown", "agent_id", agentID, "error", err)
		}
	}
}

func (s *service) SubmitTurn(ctx context.Context, req TurnSubmission) error {
	agentID := strings.TrimSpace(req.AgentID)
	waitForSession, err := s.submitAgentReadyState(agentID)
	if err != nil {
		return err
	}
	if waitForSession {
		if err := s.waitForSubmitSessionReady(ctx, agentID); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := s.lookupAgentLocked(agentID)
	if err != nil {
		return err
	}
	if agent.cmd == nil {
		return fmt.Errorf("agent %q is not running", agent.id)
	}
	if agent.stopRequested {
		return fmt.Errorf("agent %q is stopping", agent.id)
	}
	req.AgentID = agentID
	agent.queue.Enqueue(req)
	if agent.state == agentdto.StateIdle {
		if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnEnqueued); err != nil {
			return err
		}
	}
	return nil
}

func (s *service) ListAgents(ctx context.Context) ([]AgentSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshots := make([]AgentSnapshot, 0, len(s.agents))
	for _, agent := range s.agents {
		snapshots = append(snapshots, s.snapshotLocked(ctx, agent))
	}
	sort.SliceStable(snapshots, func(i, j int) bool {
		if snapshots[i].ID != snapshots[j].ID {
			return snapshots[i].ID < snapshots[j].ID
		}
		if snapshots[i].Name != snapshots[j].Name {
			return snapshots[i].Name < snapshots[j].Name
		}
		return snapshots[i].Port < snapshots[j].Port
	})
	return snapshots, nil
}

func (s *service) Snapshot(ctx context.Context, agentID string) (AgentSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agent, err := s.lookupAgentLocked(agentID)
	if err != nil {
		return AgentSnapshot{}, err
	}
	return s.snapshotLocked(ctx, agent), nil
}

func (s *service) GetAgentSnapshot(agentID string) (*AgentSnapshot, error) {
	snapshot, err := s.Snapshot(context.Background(), strings.TrimSpace(agentID))
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (s *service) CompleteTurn(ctx context.Context, agentID, turnID string, success bool, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := s.lookupAgentLocked(strings.TrimSpace(agentID))
	if err != nil {
		return err
	}
	turnID = strings.TrimSpace(turnID)
	if agent.activeTurnID == "" {
		return fmt.Errorf("%w: agent %q has no active turn", errTurnNotActive, agent.id)
	}
	if turnID != "" && agent.activeTurnID != turnID {
		return fmt.Errorf("%w: turn %q is not active on agent %q", errTurnNotActive, turnID, agentID)
	}
	trigger := agentdto.TriggerTurnCompleted
	if success {
		agent.lastError = ""
	} else {
		trigger = agentdto.TriggerTurnAborted
		agent.lastError = strings.TrimSpace(errMsg)
	}
	if err := s.fireOrForceLocked(ctx, agent, trigger); err != nil {
		return err
	}
	agent.activeTurnID = ""
	return nil
}
