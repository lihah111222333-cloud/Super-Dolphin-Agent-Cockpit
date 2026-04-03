package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
)

type launcherLaunchAttempt struct {
	agentID     string
	expectedSeq uint64
	launching   agentRuntime
}

func (s *service) launchAgentViaLauncher(ctx context.Context, req LaunchRequest) error {
	attempt, handled, err := s.prepareLauncherLaunch(ctx, req)
	if handled || err != nil {
		return err
	}
	result, launchErr := s.launcher.Launch(ctx, &attempt.launching, req)
	return s.finishLauncherLaunch(ctx, attempt, result, launchErr)
}

func (s *service) prepareLauncherLaunch(ctx context.Context, req LaunchRequest) (launcherLaunchAttempt, bool, error) {
	if err := validateLaunchRequest(req); err != nil {
		return launcherLaunchAttempt{}, true, err
	}
	s.mu.Lock()
	agent := s.agentForLaunchLocked(req)
	if launchInProgress(ctx, s, agent) {
		s.mu.Unlock()
		return launcherLaunchAttempt{}, true, fmt.Errorf("agent %q already launched", agent.id)
	}
	agent.queue.Clear()
	if err := s.prepareLaunchStateLocked(ctx, agent); err != nil {
		s.mu.Unlock()
		return launcherLaunchAttempt{}, true, err
	}
	if s.launcher == nil {
		err := s.startProcessLocked(ctx, agent)
		s.mu.Unlock()
		return launcherLaunchAttempt{}, true, err
	}
	agent.launchSeq++
	attempt := launcherLaunchAttempt{
		agentID:     agent.id,
		expectedSeq: agent.launchSeq,
		launching:   *agent,
	}
	s.mu.Unlock()
	return attempt, false, nil
}

func launchInProgress(ctx context.Context, s *service, agent *agentRuntime) bool {
	if s.agentRunningLocked(ctx, agent) {
		return true
	}
	return agent.launchSeq > agent.lastExitedSeq &&
		(agent.state == agentdto.StateProvisioning || agent.state == agentdto.StateRecovering)
}

func (s *service) finishLauncherLaunch(ctx context.Context, attempt launcherLaunchAttempt, result LaunchResult, launchErr error) error {
	s.mu.Lock()
	agent, err := s.lookupAgentLocked(attempt.agentID)
	if err != nil || agent.launchSeq != attempt.expectedSeq {
		s.mu.Unlock()
		return s.discardStaleLaunchResult(ctx, &attempt.launching, launchErr)
	}
	if launchErr != nil {
		return s.failLauncherLaunchLocked(ctx, agent, &attempt.launching, launchErr)
	}
	return s.completeLauncherLaunchLocked(ctx, agent, &attempt.launching, result)
}

func (s *service) discardStaleLaunchResult(ctx context.Context, launching *agentRuntime, launchErr error) error {
	if launchErr == nil {
		_ = s.launcher.Stop(ctx, launching)
	}
	return launchErr
}

func (s *service) failLauncherLaunchLocked(ctx context.Context, agent, launching *agentRuntime, launchErr error) error {
	agent.lastError = firstTrimmed(launching.lastError, launchErr.Error())
	if fireErr := s.fireOrForceLocked(ctx, agent, agentdto.TriggerLaunchFailed); fireErr != nil {
		s.mu.Unlock()
		return errors.Join(launchErr, fireErr)
	}
	s.mu.Unlock()
	return launchErr
}

func (s *service) completeLauncherLaunchLocked(ctx context.Context, agent, launching *agentRuntime, result LaunchResult) error {
	adoptLaunchStateLocked(agent, launching)
	bindLaunchResult(agent, result)
	if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerLaunchSucceeded); err != nil {
		agent.lastError = err.Error()
		agent.cmd = nil
		agent.threadID = ""
		resetRuntimeStateLocked(agent)
		s.mu.Unlock()
		_ = s.launcher.Stop(ctx, launching)
		return err
	}
	s.publishAgentLaunched(agent)
	s.mu.Unlock()
	return nil
}

func (s *service) stopAgentViaLauncher(ctx context.Context, agentID, reason string) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return errAgentNotFound
	}
	if !s.shouldStopViaLauncher(ctx, agentID) {
		return s.stopAgentWithReason(ctx, agentID, reason)
	}
	agent, launchSeq, err := s.prepareLauncherStop(ctx, agentID, reason)
	if err != nil {
		return err
	}
	if agent == nil {
		return nil
	}
	if err := s.launcher.Stop(ctx, agent); err != nil {
		return err
	}
	s.handleProcessExit(ctx, agentID, launchSeq, nil)
	return nil
}

func (s *service) shouldStopViaLauncher(ctx context.Context, agentID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	agent, err := s.lookupAgentLocked(agentID)
	if err != nil || agent == nil || s.launcher == nil || agent.cmd != nil {
		return false
	}
	return s.launcher.IsRunning(ctx, agent)
}

func (s *service) prepareLauncherStop(ctx context.Context, agentID, reason string) (*agentRuntime, uint64, error) {
	s.mu.Lock()
	agent, err := s.lookupAgentLocked(agentID)
	if err != nil {
		s.mu.Unlock()
		return nil, 0, err
	}
	if !s.agentRunningLocked(ctx, agent) {
		s.mu.Unlock()
		return nil, 0, fmt.Errorf("agent %q is not running", agent.id)
	}
	if agent.stopRequested {
		s.mu.Unlock()
		return nil, 0, nil
	}
	if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerStopRequested); err != nil {
		s.mu.Unlock()
		return nil, 0, err
	}
	agent.stopRequested = true
	setStopReasonIfEmpty(agent, reason)
	agent.queue.Clear()
	agent.activeTurnID = ""
	agent.threadID = ""
	launchSeq := agent.launchSeq
	s.mu.Unlock()
	return agent, launchSeq, nil
}

func (s *service) submitTurnViaLauncher(ctx context.Context, req TurnSubmission) error {
	agentID, err := submissionAgentID(req)
	if err != nil {
		return err
	}
	handled, err := s.trySubmitRemoteTurn(ctx, agentID, req)
	if handled {
		return err
	}
	if err != nil {
		return err
	}
	return s.enqueueLocalTurnSubmission(ctx, agentID, req)
}

func submissionAgentID(req TurnSubmission) (string, error) {
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		return "", errors.New("agent id is required")
	}
	return agentID, nil
}

func (s *service) trySubmitRemoteTurn(ctx context.Context, agentID string, req TurnSubmission) (bool, error) {
	s.mu.Lock()
	agent, err := s.lookupAgentLocked(agentID)
	if err != nil {
		s.mu.Unlock()
		return true, err
	}
	if !s.canSubmitViaLauncher(ctx, agent) {
		s.mu.Unlock()
		return false, nil
	}
	if agent.stopRequested {
		s.mu.Unlock()
		return true, fmt.Errorf("agent %q is stopping", agent.id)
	}
	if remoteAgentBusy(agent) {
		s.mu.Unlock()
		return true, fmt.Errorf("agent %q is busy", agent.id)
	}
	req.AgentID = agentID
	turnID, err := s.launcher.SubmitTurn(ctx, agent, req)
	if err != nil {
		agent.lastError = err.Error()
		s.mu.Unlock()
		return true, err
	}
	if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnEnqueued); err != nil {
		s.mu.Unlock()
		return true, err
	}
	agent.activeTurnID = firstTrimmed(turnID, req.ExpectedTurnID)
	if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted); err != nil {
		s.mu.Unlock()
		return true, err
	}
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt, agent.startedAt)
	s.mu.Unlock()
	return true, nil
}

func (s *service) canSubmitViaLauncher(ctx context.Context, agent *agentRuntime) bool {
	return s.launcher != nil && agent.cmd == nil && s.launcher.IsRunning(ctx, agent)
}

func remoteAgentBusy(agent *agentRuntime) bool {
	return agent.state != agentdto.StateIdle || agent.activeTurnID != ""
}

func (s *service) enqueueLocalTurnSubmission(ctx context.Context, agentID string, req TurnSubmission) error {
	waitForSession, err := s.submitAgentReadyState(ctx, agentID)
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

func (s *service) agentRunningLocked(ctx context.Context, agent *agentRuntime) bool {
	if agent == nil {
		return false
	}
	if s.launcher != nil {
		return s.launcher.IsRunning(ctx, agent)
	}
	return agent.cmd != nil
}

func adoptLaunchStateLocked(dst, src *agentRuntime) {
	if dst == nil || src == nil {
		return
	}
	dst.cmd = src.cmd
	dst.monitoredSeq = src.monitoredSeq
	dst.stopRequested = src.stopRequested
	dst.activeTurnID = src.activeTurnID
	dst.threadID = src.threadID
	dst.remoteThreadID = src.remoteThreadID
	dst.remoteAgentID = src.remoteAgentID
	dst.startedAt = src.startedAt
	dst.updatedAt = src.updatedAt
	dst.exitedAt = cloneTime(src.exitedAt)
	dst.lastError = src.lastError
}

func bindLaunchResult(agent *agentRuntime, result LaunchResult) {
	if agent == nil {
		return
	}
	if threadID := strings.TrimSpace(result.ThreadID); threadID != "" {
		agent.threadID = threadID
		agent.remoteThreadID = threadID
	}
	if remoteAgentID := strings.TrimSpace(result.RemoteAgentID); remoteAgentID != "" {
		agent.remoteAgentID = remoteAgentID
	}
}
