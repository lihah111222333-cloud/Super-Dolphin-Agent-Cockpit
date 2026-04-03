package orchestration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
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
		if !s.agentRunningLocked(ctx, agent) || agent.stopRequested || agent.state != agentdto.StateTurnQueued {
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
	if agent.stopRequested && strings.TrimSpace(agent.stopReason) != "" {
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
	waitCtx, cancel := platformconfig.WithTimeout(ctx, s.processExitWaitTimeout)
	defer cancel()
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
		case <-waitCtx.Done():
			if ctx != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			return s.forceKillProcess(agentID, launchSeq)
		case <-ticker.C:
		}
	}
}

func (s *service) forceKillProcess(agentID string, launchSeq uint64) error {
	var proc *os.Process
	s.mu.RLock()
	if agent, ok := s.agents[agentID]; ok && agent.launchSeq == launchSeq && agent.lastExitedSeq < launchSeq && agent.cmd != nil {
		proc = agent.cmd.Process
	}
	s.mu.RUnlock()
	if proc == nil {
		return fmt.Errorf("orchestration: timed out waiting for process exit for agent %q", agentID)
	}
	if err := proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		s.logger.Warn("orchestration: failed to force-kill timed out agent process", "agent_id", agentID, "launch_seq", launchSeq, "error", err)
		return fmt.Errorf("orchestration: failed to force-kill timed out agent process %q: %w", agentID, err)
	}
	s.logger.Warn("orchestration: timed out waiting for process exit; forced kill issued", "agent_id", agentID, "launch_seq", launchSeq, "timeout", s.processExitWaitTimeout)
	return nil
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
	result := waitResult{agentID: target.agentID, launchSeq: target.launchSeq, err: err}
	if ctx.Err() != nil {
		// Runner shutdown still needs the exit transition to complete so StopAllAgents can observe it.
		a.service.handleProcessExit(context.Background(), result.agentID, result.launchSeq, result.err)
		return
	}
	select {
	case results <- result:
	case <-ctx.Done():
		a.service.handleProcessExit(context.Background(), result.agentID, result.launchSeq, result.err)
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
		detectedAt := resolveEventTime(ctx, time.Now())
		stalledFor := time.Duration(0)
		if !agent.updatedAt.IsZero() && detectedAt.After(agent.updatedAt) {
			stalledFor = detectedAt.Sub(agent.updatedAt)
		}
		a.service.publishTurnStalled(&agent, agent.threadID, agent.activeTurnID, recoverReasonStall, stalledFor, detectedAt)
		if err := a.service.recoverWithReason(ctx, agent.id, recoverReasonStall); err != nil {
			a.logger.Warn("orchestration: stalled agent recovery failed", "agent_id", agent.id, "error", err)
		}
	}
}

func (a *runnerActor) stopAll() {
	a.service.StopAllAgents()
}
