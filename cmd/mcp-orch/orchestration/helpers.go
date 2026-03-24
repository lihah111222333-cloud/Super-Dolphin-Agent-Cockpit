package orchestration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/qmuntal/stateless"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformstatemachine "github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
)

const submitSessionReadyTimeout = 5 * time.Second

type sessionReadyWaiter interface {
	WaitForSessionReady(ctx context.Context, agentID string, timeout time.Duration) error
}

func buildStatesFromDefinitions(defs []agentdto.TransitionDefinition) []platformstatemachine.StateConfig {
	permits := make(map[string][]platformstatemachine.Permit, len(agentdto.StateDefinitions))
	for _, def := range defs {
		permits[def.From] = append(permits[def.From], platformstatemachine.Permit{
			Trigger: def.Trigger,
			Dest:    def.To,
		})
	}
	states := make([]platformstatemachine.StateConfig, 0, len(agentdto.StateDefinitions))
	for _, def := range agentdto.StateDefinitions {
		states = append(states, platformstatemachine.StateConfig{
			Name:    def.Name,
			Permits: permits[def.Name],
		})
	}
	return states
}

func (s *service) BindActiveTurnID(ctx context.Context, agentID, turnID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := s.lookupAgentLocked(strings.TrimSpace(agentID))
	if err != nil {
		return err
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return errors.New("turn id is required")
	}
	if agent.activeTurnID == "" {
		return fmt.Errorf("%w: agent %q has no active turn", errTurnNotActive, agent.id)
	}
	if agent.activeTurnID == turnID {
		return nil
	}
	agent.activeTurnID = turnID
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
	return nil
}

func (s *service) claimMonitorTargetsLocked() []monitorTarget {
	s.mu.Lock()
	defer s.mu.Unlock()

	targets := make([]monitorTarget, 0, len(s.agents))
	for _, agent := range s.agents {
		if agent.cmd == nil || agent.monitoredSeq == agent.launchSeq {
			continue
		}
		agent.monitoredSeq = agent.launchSeq
		targets = append(targets, monitorTarget{
			agentID:   agent.id,
			launchSeq: agent.launchSeq,
			cmd:       agent.cmd,
		})
	}
	return targets
}

func (s *service) reconcileReadyStateLocked(ctx context.Context, agent *agentRuntime) {
	if agent.cmd == nil || agent.stopRequested {
		return
	}
	if agent.activeTurnID == "" && agent.state == agentdto.StateIdle && agent.queue.Len() > 0 {
		if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnEnqueued); err != nil {
			s.logger.Warn("orchestration: failed to mark queued turn", "agent_id", agent.id, "error", err)
		}
		return
	}
	if agent.activeTurnID != "" {
		return
	}
	switch agent.state {
	case agentdto.StateTurnStarting, agentdto.StateTurnRunning:
		if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnCompleted); err != nil {
			s.logger.Warn("orchestration: failed to reconcile ready state", "agent_id", agent.id, "state", agent.state, "error", err)
		}
	}
}

func (s *service) startTurnExecution(ctx context.Context, work turnWork) {
	if s.turnStarter == nil {
		s.finishTurnStartFailure(ctx, work, errors.New("turn starter is not configured"))
		return
	}
	startedTurnID, err := s.turnStarter.StartTurn(ctx, work.submission)
	if err != nil {
		s.finishTurnStartFailure(ctx, work, err)
		return
	}
	s.finishTurnStartSuccess(ctx, work, startedTurnID)
}

func (s *service) finishTurnStartSuccess(ctx context.Context, work turnWork, startedTurnID string) {
	currentTurnID := strings.TrimSpace(startedTurnID)
	if currentTurnID == "" {
		currentTurnID = work.turnID
	}
	if currentTurnID != work.turnID {
		if err := s.BindActiveTurnID(ctx, work.agentID, currentTurnID); err != nil {
			s.logger.Warn("orchestration: failed to bind started turn id", "agent_id", work.agentID, "turn_id", currentTurnID, "error", err)
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := s.lookupAgentLocked(work.agentID)
	if err != nil || agent.activeTurnID != currentTurnID {
		return
	}
	if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted); err != nil {
		s.logger.Warn("orchestration: failed to mark turn running", "agent_id", agent.id, "turn_id", currentTurnID, "error", err)
	}
}

func (s *service) finishTurnStartFailure(ctx context.Context, work turnWork, startErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := s.lookupAgentLocked(work.agentID)
	if err != nil || agent.activeTurnID != work.turnID {
		return
	}
	agent.lastError = startErr.Error()
	agent.activeTurnID = ""
	if agent.state != agentdto.StateTurnStarting {
		return
	}
	if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnCompleted); err != nil {
		s.logger.Warn("orchestration: failed to reset turn after start error", "agent_id", agent.id, "turn_id", work.turnID, "error", err)
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

func (s *service) listAgents() []agentRuntime {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agents := make([]agentRuntime, 0, len(s.agents))
	for _, agent := range s.agents {
		snapshot := *agent
		snapshot.cmd = nil
		snapshot.queue = nil
		snapshot.sm = nil
		snapshot.exitedAt = cloneTime(agent.exitedAt)
		agents = append(agents, snapshot)
	}
	return agents
}

func (s *service) lookupAgentLocked(agentID string) (*agentRuntime, error) {
	agent, ok := s.agents[agentID]
	if ok {
		return agent, nil
	}
	return nil, fmt.Errorf("%w: %s", errAgentNotFound, agentID)
}

func cloneTurnSubmission(sub turndto.TurnSubmission) turndto.TurnSubmission {
	cloned := sub
	cloned.Inputs = append([]turndto.InputItem(nil), sub.Inputs...)
	cloned.SelectedSkills = append([]string(nil), sub.SelectedSkills...)
	cloned.OutputSchema = append([]byte(nil), sub.OutputSchema...)
	return cloned
}

type SubmissionQueue struct {
	mu    sync.Mutex
	items []turndto.TurnSubmission
}

func (q *SubmissionQueue) Enqueue(s turndto.TurnSubmission) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, cloneTurnSubmission(s))
}

func (q *SubmissionQueue) Prepend(s turndto.TurnSubmission) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append([]turndto.TurnSubmission{cloneTurnSubmission(s)}, q.items...)
}

func (q *SubmissionQueue) Dequeue() (turndto.TurnSubmission, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return turndto.TurnSubmission{}, false
	}
	s := q.items[0]
	q.items = q.items[1:]
	return cloneTurnSubmission(s), true
}

// Peek is currently used only in tests; kept for diagnostic/debugging use.
func (q *SubmissionQueue) Peek() (turndto.TurnSubmission, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return turndto.TurnSubmission{}, false
	}
	return cloneTurnSubmission(q.items[0]), true
}

func (q *SubmissionQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func (q *SubmissionQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = nil
}
