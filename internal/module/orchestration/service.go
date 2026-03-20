package orchestration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/kelindar/event"
	"github.com/qmuntal/stateless"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformstatemachine "github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
)

var errAgentNotFound = errors.New("agent not found")

type service struct {
	logger      *slog.Logger
	eventBus    *event.Dispatcher
	machineCfg  platformstatemachine.Config
	mu          sync.RWMutex
	agents      map[string]*agentRuntime
	nextTurnSeq int64
}

type agentRuntime struct {
	id            string
	name          string
	parentID      string
	cwd           string
	command       []string
	env           []string
	state         string
	threadID      string
	activeTurnID  string
	lastError     string
	startedAt     time.Time
	updatedAt     time.Time
	exitedAt      *time.Time
	launchSeq     uint64
	monitoredSeq  uint64
	stopRequested bool
	cmd           *exec.Cmd
	queue         *SubmissionQueue
	sm            *stateless.StateMachine
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

func NewService(logger *slog.Logger, eventBus *event.Dispatcher) *service {
	return &service{
		logger:   logger,
		eventBus: eventBus,
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
	s.publishAgentStopped(agent, "user_requested")
	return nil
}

func (s *service) StopAllAgents() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, agent := range s.agents {
		if err := s.stopAgentLocked(context.Background(), agent); err == nil {
			s.publishAgentStopped(agent, "shutdown")
		}
	}
}

func (s *service) stopAgentLocked(ctx context.Context, agent *agentRuntime) error {
	agent.stopRequested = true
	agent.queue.Clear()
	agent.activeTurnID = ""
	agent.threadID = ""
	s.fireOrForceLocked(ctx, agent, agentdto.TriggerStopRequested, agentdto.StateStopping)
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
		s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnEnqueued, agentdto.StateTurnQueued)
	}
	return nil
}

func (s *service) Snapshot(ctx context.Context, agentID string) (AgentSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agent, err := s.lookupAgentLocked(agentID)
	if err != nil {
		return AgentSnapshot{}, err
	}

	return AgentSnapshot{
		AgentID:         agent.id,
		Name:            agent.name,
		ParentID:        agent.parentID,
		Cwd:             agent.cwd,
		PID:             processID(agent.cmd),
		State:           agent.state,
		ThreadID:        agent.threadID,
		ActiveTurnID:    agent.activeTurnID,
		PendingTurns:    agent.queue.Len(),
		Command:         append([]string(nil), agent.command...),
		AllowedTriggers: platformstatemachine.AllowedTriggers(agent.sm, ctx),
		LastError:       agent.lastError,
		StartedAt:       agent.startedAt,
		UpdatedAt:       agent.updatedAt,
		ExitedAt:        cloneTime(agent.exitedAt),
	}, nil
}

func (s *service) startProcessLocked(ctx context.Context, agent *agentRuntime) error {
	cmd := exec.Command(agent.command[0], agent.command[1:]...)
	cmd.Dir = agent.cwd
	cmd.Env = append(os.Environ(), agent.env...)
	if err := cmd.Start(); err != nil {
		agent.lastError = err.Error()
		s.fireOrForceLocked(ctx, agent, agentdto.TriggerLaunchFailed, agentdto.StateFailed)
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
	s.fireOrForceLocked(ctx, agent, agentdto.TriggerLaunchSucceeded, agentdto.StateIdle)
	s.logger.Info("orchestration: agent launched", "agent_id", agent.id, "pid", cmd.Process.Pid)
	s.publishAgentLaunched(agent)
	return nil
}

func (s *service) fireOrForceLocked(ctx context.Context, agent *agentRuntime, trigger, fallback string) {
	if err := s.fireAndPublishLocked(ctx, agent, trigger); err == nil {
		return
	}
	s.forceStateLocked(agent, fallback, trigger)
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

func (s *service) forceStateLocked(agent *agentRuntime, next, trigger string) {
	before := agent.state
	agent.state = next
	agent.updatedAt = time.Now()
	s.publishStateChanged(agent, before, trigger)
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
		s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted, agentdto.StateTurnStarting)
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

	agent, err := s.lookupAgentLocked(agentID)
	if err != nil {
		return err
	}
	if agent.activeTurnID != turnID {
		return fmt.Errorf("turn %q is not active on agent %q", turnID, agentID)
	}
	agent.activeTurnID = ""
	if success {
		s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnCompleted, agentdto.StateIdle)
	} else {
		agent.lastError = errMsg
		s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAborted, agentdto.StateIdle)
	}
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
	if err != nil {
		agent.lastError = err.Error()
	}
	if err != nil && !agent.stopRequested {
		s.publishAgentFailed(agent, err.Error(), true)
	}

	if agent.stopRequested {
		s.fireOrForceLocked(ctx, agent, agentdto.TriggerProcessExited, agentdto.StateStopped)
		return
	}
	switch agent.state {
	case agentdto.StateProvisioning, agentdto.StateRecovering:
		s.fireOrForceLocked(ctx, agent, agentdto.TriggerLaunchFailed, agentdto.StateFailed)
	default:
		s.fireOrForceLocked(ctx, agent, agentdto.TriggerProcessExited, agentdto.StateFailed)
		return
	}
}
