package orchestration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kelindar/event"
	"github.com/qmuntal/stateless"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformstatemachine "github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
	"github.com/anthropic-ai/super-agent-v3/internal/store/taskdag"
)

var (
	errAgentNotFound          = errors.New("agent not found")
	errIllegalStateTransition = errors.New("illegal state transition")
	errTurnNotActive          = errors.New("turn is not active")
)

const submitSessionReadyTimeout = 5 * time.Second

type sessionReadyWaiter interface {
	WaitForSessionReady(ctx context.Context, agentID string, timeout time.Duration) error
}

type service struct {
	logger         *slog.Logger
	eventBus       *event.Dispatcher
	sessionCleaner SessionCleaner
	turnStarter    TurnStarter
	dagStore       taskdag.Store
	recoveryStore  recoveryTurnStore
	machineCfg     platformstatemachine.Config
	mu             sync.RWMutex
	agents         map[string]*agentRuntime
	nextTurnSeq    int64
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
		logger = slog.Default()
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
		agents: make(map[string]*agentRuntime),
	}
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
	s.mu.Lock()

	agent, err := s.lookupAgentLocked(agentID)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	launchSeq := agent.launchSeq
	if err := s.stopAgentLocked(ctx, agent, "user_requested"); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()

	return s.waitForProcessExit(ctx, agent.id, launchSeq)
}

func (s *service) StopAllAgents() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, agent := range s.agents {
		if err := s.stopAgentLocked(context.Background(), agent, "shutdown"); err == nil {
			s.removeSession(agent)
			s.publishAgentStopped(agent, "shutdown")
			agent.stopReason = ""
		}
	}
}

func (s *service) stopAgentLocked(ctx context.Context, agent *agentRuntime, reason string) error {
	if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerStopRequested); err != nil {
		return err
	}
	agent.stopRequested = true
	agent.stopReason = strings.TrimSpace(reason)
	agent.queue.Clear()
	agent.activeTurnID = ""
	agent.threadID = ""
	return stopProcess(agent.cmd)
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

func (s *service) submitAgentReadyState(agentID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agent, err := s.lookupAgentLocked(agentID)
	if err != nil {
		return false, err
	}
	if agent.cmd == nil {
		return false, fmt.Errorf("agent %q is not running", agent.id)
	}
	if agent.stopRequested {
		return false, fmt.Errorf("agent %q is stopping", agent.id)
	}
	return agent.state == agentdto.StateIdle && agent.activeTurnID == "" && agent.queue.Len() == 0, nil
}

func (s *service) waitForSubmitSessionReady(ctx context.Context, agentID string) error {
	if s == nil || s.turnStarter == nil {
		return nil
	}
	waiter, ok := s.turnStarter.(sessionReadyWaiter)
	if !ok {
		return nil
	}
	return waiter.WaitForSessionReady(ctx, agentID, submitSessionReadyTimeout)
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

func (s *service) startProcessLocked(ctx context.Context, agent *agentRuntime) error {
	cmd := exec.Command(agent.command[0], agent.command[1:]...)
	cmd.Dir = agent.cwd
	cmd.Env = append(os.Environ(), agent.env...)
	if err := cmd.Start(); err != nil {
		agent.lastError = err.Error()
		if fireErr := s.fireOrForceLocked(ctx, agent, agentdto.TriggerLaunchFailed); fireErr != nil {
			return errors.Join(err, fireErr)
		}
		return err
	}
	now := resolveEventTime(ctx, agent.updatedAt)
	agent.cmd = cmd
	agent.launchSeq++
	agent.monitoredSeq = 0
	agent.stopRequested = false
	agent.activeTurnID = ""
	agent.threadID = ""
	agent.startedAt = now
	agent.updatedAt = now
	agent.exitedAt = nil
	if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerLaunchSucceeded); err != nil {
		agent.lastError = err.Error()
		_ = stopProcess(cmd)
		agent.cmd = nil
		return err
	}
	s.logger.Info("orchestration: agent launched", "agent_id", agent.id, "pid", cmd.Process.Pid)
	s.publishAgentLaunched(agent)
	return nil
}

func (s *service) fireOrForceLocked(ctx context.Context, agent *agentRuntime, trigger string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	before := ""
	if agent != nil {
		before = agent.state
	}
	if agent == nil || agent.sm == nil {
		return formatIllegalTransitionError(ctx, agent, before, trigger, errors.New("state machine is not initialized"))
	}
	if err := s.fireAndPublishLocked(ctx, agent, trigger); err != nil {
		return formatIllegalTransitionError(ctx, agent, before, trigger, err)
	}
	return nil
}

func formatIllegalTransitionError(ctx context.Context, agent *agentRuntime, before, trigger string, err error) error {
	allowed := allowedTriggersForState(ctx, agent, before)
	agentID := ""
	if agent != nil {
		agentID = agent.id
	}
	return fmt.Errorf("%w for agent %q: state=%s trigger=%s allowed=%v: %w", errIllegalStateTransition, agentID, before, trigger, allowed, err)
}

func allowedTriggersForState(ctx context.Context, agent *agentRuntime, state string) []string {
	if ctx == nil {
		ctx = context.Background()
	}
	if agent != nil && agent.sm != nil {
		if allowed := platformstatemachine.AllowedTriggers(agent.sm, ctx); allowed != nil {
			return allowed
		}
	}
	if strings.TrimSpace(state) == "" {
		return nil
	}
	return agentdto.AllowedTriggers(state)
}

func (s *service) fireAndPublishLocked(ctx context.Context, agent *agentRuntime, trigger string) error {
	before := agent.state
	if err := agent.sm.FireCtx(ctx, stateless.Trigger(trigger)); err != nil {
		return err
	}
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt, agent.startedAt)
	s.publishStateChanged(agent, before, trigger)
	return nil
}

func (s *service) claimTurnWork(ctx context.Context) []turnWork {
	s.mu.Lock()
	defer s.mu.Unlock()

	work := make([]turnWork, 0, len(s.agents))
	for _, agent := range s.agents {
		s.reconcileReadyStateLocked(ctx, agent)
		if agent.cmd == nil || agent.stopRequested || agent.state != agentdto.StateTurnQueued {
			continue
		}
		submission, ok := agent.queue.Dequeue()
		if !ok {
			continue
		}
		if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted); err != nil {
			agent.queue.Enqueue(submission)
			s.logger.Warn("orchestration: failed to accept queued turn", "agent_id", agent.id, "error", err)
			continue
		}
		turnID := s.turnIDFor(submission)
		submission.ExpectedTurnID = turnID
		if threadID := strings.TrimSpace(submission.ThreadID); threadID != "" {
			agent.threadID = threadID
		}
		agent.activeTurnID = turnID
		work = append(work, turnWork{
			agentID:    agent.id,
			threadID:   submission.ThreadID,
			turnID:     turnID,
			submission: submission,
		})
	}
	return work
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

func (s *service) handleProcessExit(ctx context.Context, agentID string, launchSeq uint64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, lookupErr := s.lookupAgentLocked(agentID)
	if lookupErr != nil || agent.launchSeq != launchSeq {
		return
	}
	now := resolveEventTime(ctx, agent.updatedAt, agent.startedAt)
	agent.cmd = nil
	agent.exitedAt = &now
	agent.lastExitedSeq = launchSeq
	agent.updatedAt = now
	resetRuntimeStateLocked(agent)
	s.removeSession(agent)
	s.recordProcessExitError(agent, err)
	s.handleProcessExitTransition(ctx, agent)
	if agent.stopRequested && agent.stopReason == "user_requested" {
		s.publishAgentStopped(agent, agent.stopReason)
	}
	agent.stopReason = ""
}

func (s *service) recordProcessExitError(agent *agentRuntime, err error) {
	if err == nil {
		return
	}
	agent.lastError = err.Error()
	if !agent.stopRequested {
		s.publishAgentFailed(agent, err.Error(), true)
	}
}

func (s *service) handleProcessExitTransition(ctx context.Context, agent *agentRuntime) {
	trigger := agentdto.TriggerProcessExited
	message := "orchestration: failed to mark agent failed after process exit"
	if agent.stopRequested {
		message = "orchestration: failed to mark agent stopped after process exit"
	} else if agent.state == agentdto.StateProvisioning || agent.state == agentdto.StateRecovering {
		trigger = agentdto.TriggerLaunchFailed
		message = "orchestration: failed to mark launch failure after process exit"
	}
	if fireErr := s.fireOrForceLocked(ctx, agent, trigger); fireErr != nil {
		s.logger.Warn(message, "agent_id", agent.id, "error", fireErr)
	}
}

func (s *service) waitForProcessExit(ctx context.Context, agentID string, launchSeq uint64) error {
	if launchSeq == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		s.mu.RLock()
		agent, ok := s.agents[agentID]
		exited := ok && agent.lastExitedSeq >= launchSeq
		s.mu.RUnlock()
		if exited {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
