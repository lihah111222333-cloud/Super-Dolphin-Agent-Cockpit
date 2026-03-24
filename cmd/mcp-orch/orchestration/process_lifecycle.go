package orchestration

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
)

type generationAwareSessionCleaner interface {
	RemoveSessionGeneration(agentID string, generation uint64)
}

func (s *service) BindSessionGeneration(ctx context.Context, agentID string, generation uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := s.lookupAgentLocked(strings.TrimSpace(agentID))
	if err != nil {
		return err
	}
	if generation == 0 {
		return errors.New("session generation is required")
	}
	agent.sessionGeneration = generation
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
	return nil
}

func (s *service) removeSession(agent *agentRuntime) {
	if s.sessionCleaner == nil || agent == nil {
		return
	}
	if cleaner, ok := s.sessionCleaner.(generationAwareSessionCleaner); ok {
		if agent.sessionGeneration != 0 {
			cleaner.RemoveSessionGeneration(agent.id, agent.sessionGeneration)
			agent.sessionGeneration = 0
		}
		return
	}
	s.sessionCleaner.RemoveSession(agent.id)
	agent.sessionGeneration = 0
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

type runnerActor struct {
	logger  *slog.Logger
	service *service
}

type waitResult struct {
	agentID   string
	launchSeq uint64
	err       error
}

func NewRunnerActor(logger *slog.Logger, service *service) platformrunner.Runner {
	return &runnerActor{logger: logger, service: service}
}

func (a *runnerActor) Run(ctx context.Context) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	stallDetector := &StallDetector{threshold: 30 * time.Second, logger: a.logger}
	results := make(chan waitResult, 32)
	for {
		a.startWaiters(ctx, results)
		a.processTurnQueues(ctx)

		select {
		case <-ctx.Done():
			a.stopAll()
			return ctx.Err()
		case result := <-results:
			a.service.handleProcessExit(ctx, result.agentID, result.launchSeq, result.err)
		case <-ticker.C:
			a.recoverStalledAgents(ctx, stallDetector)
		}
	}
}

func (a *runnerActor) startWaiters(ctx context.Context, results chan<- waitResult) {
	for _, target := range a.service.claimMonitorTargetsLocked() {
		go a.waitForExit(ctx, target, results)
	}
}

func (a *runnerActor) waitForExit(ctx context.Context, target monitorTarget, results chan<- waitResult) {
	err := target.cmd.Wait()
	select {
	case <-ctx.Done():
	case results <- waitResult{agentID: target.agentID, launchSeq: target.launchSeq, err: err}:
	}
}

func (a *runnerActor) processTurnQueues(ctx context.Context) {
	for _, work := range a.service.claimTurnWork(ctx) {
		a.service.startTurnExecution(ctx, work)
	}
}

func (a *runnerActor) recoverStalledAgents(ctx context.Context, stallDetector *StallDetector) {
	for _, agent := range a.service.listAgents() {
		if !stallDetector.CheckStall(&agent) {
			continue
		}
		if err := a.service.Recover(ctx, agent.id); err != nil {
			a.logger.Warn("orchestration: stalled agent recovery failed", "agent_id", agent.id, "error", err)
		}
	}
}

func (a *runnerActor) stopAll() {
	a.service.StopAllAgents()
}
