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

type service struct {
	logger         *slog.Logger
	eventBus       *event.Dispatcher
	sessionCleaner SessionCleaner
	dagStore       taskdag.Store
	machineCfg     platformstatemachine.Config
	mu             sync.RWMutex
	agents         map[string]*agentRuntime
	nextTurnSeq    int64
}

type agentRuntime struct {
	id               string
	name             string
	parentID         string
	cwd              string
	command          []string
	env              []string
	port             int
	provider         string
	state            string
	threadID         string
	activeTurnID     string
	lastReport       string
	reportRequesters []string
	lastError        string
	startedAt        time.Time
	updatedAt        time.Time
	exitedAt         *time.Time
	launchSeq        uint64
	monitoredSeq     uint64
	stopRequested    bool
	cmd              *exec.Cmd
	queue            *SubmissionQueue
	sm               *stateless.StateMachine
}

type monitorTarget struct {
	agentID   string
	launchSeq uint64
	cmd       *exec.Cmd
}

type turnWork struct {
	agentID  string
	threadID string
	turnID   string
}

func NewService(logger *slog.Logger, eventBus *event.Dispatcher, sessionCleaner SessionCleaner, dagStore taskdag.Store) *service {
	if logger == nil {
		logger = slog.Default()
	}
	return &service{
		logger:         logger,
		eventBus:       eventBus,
		sessionCleaner: sessionCleaner,
		dagStore:       dagStore,
		machineCfg: platformstatemachine.Config{
			Initial: agentdto.StateProvisioning,
			States:  buildStatesFromDefinitions(agentdto.TransitionDefinitions),
		},
		agents: make(map[string]*agentRuntime),
	}
}

func (s *service) removeSession(agentID string) {
	if s.sessionCleaner != nil {
		s.sessionCleaner.RemoveSession(agentID)
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
	s.prepareLaunchStateLocked(agent)
	return s.startProcessLocked(ctx, agent)
}

func (s *service) StopAgent(ctx context.Context, agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := s.lookupAgentLocked(agentID)
	if err != nil {
		return err
	}
	if err := s.stopAgentLocked(ctx, agent); err != nil {
		return err
	}
	s.removeSession(agent.id)
	s.publishAgentStopped(agent, "user_requested")
	return nil
}

func (s *service) StopAllAgents() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, agent := range s.agents {
		if err := s.stopAgentLocked(context.Background(), agent); err == nil {
			s.removeSession(agent.id)
			s.publishAgentStopped(agent, "shutdown")
		}
	}
}

func (s *service) stopAgentLocked(ctx context.Context, agent *agentRuntime) error {
	if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerStopRequested); err != nil {
		return err
	}
	agent.stopRequested = true
	agent.queue.Clear()
	agent.activeTurnID = ""
	agent.threadID = ""
	return stopProcess(agent.cmd)
}

func (s *service) SubmitTurn(ctx context.Context, req TurnSubmission) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := s.lookupAgentLocked(strings.TrimSpace(req.AgentID))
	if err != nil {
		return err
	}
	if agent.cmd == nil {
		return fmt.Errorf("agent %q is not running", agent.id)
	}
	if agent.stopRequested {
		return fmt.Errorf("agent %q is stopping", agent.id)
	}
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

func (s *service) snapshotLocked(_ context.Context, agent *agentRuntime) AgentSnapshot {
	return AgentSnapshot{
		ID:         agent.id,
		Name:       agent.name,
		ParentID:   agent.parentID,
		Port:       agent.port,
		ThreadID:   agent.threadID,
		Cwd:        agent.cwd,
		State:      agent.state,
		Provider:   agent.provider,
		LastReport: agent.lastReport,
	}
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
	now := time.Now()
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
	if agent == nil || agent.sm == nil {
		return fmt.Errorf("%w: state machine is not initialized", errIllegalStateTransition)
	}
	before := agent.state
	if err := s.fireAndPublishLocked(ctx, agent, trigger); err != nil {
		allowed := platformstatemachine.AllowedTriggers(agent.sm, ctx)
		return fmt.Errorf("%w for agent %q: state=%s trigger=%s allowed=%v: %w", errIllegalStateTransition, agent.id, before, trigger, allowed, err)
	}
	return nil
}

func (s *service) fireAndPublishLocked(ctx context.Context, agent *agentRuntime, trigger string) error {
	before := agent.state
	if err := agent.sm.FireCtx(ctx, stateless.Trigger(trigger)); err != nil {
		return err
	}
	agent.updatedAt = time.Now()
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
		agent.threadID = submission.ThreadID
		agent.activeTurnID = turnID
		work = append(work, turnWork{
			agentID:  agent.id,
			threadID: submission.ThreadID,
			turnID:   turnID,
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
	now := time.Now()
	agent.cmd = nil
	agent.exitedAt = &now
	agent.updatedAt = now
	s.removeSession(agent.id)
	s.recordProcessExitError(agent, err)
	s.handleProcessExitTransition(ctx, agent)
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
