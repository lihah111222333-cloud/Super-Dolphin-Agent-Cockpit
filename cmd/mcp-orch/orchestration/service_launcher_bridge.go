package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type launcherLaunchAttempt struct {
	agentID     string
	expectedSeq uint64
	launching   agentRuntime
}

type launchedAgent struct {
	agentID string
	result  LaunchResult
}

const (
	maxLaunchRetries = 3
	launchRetryBase  = 2 * time.Second
)

func waitRetryBackoff(ctx context.Context, attempt int, agentID string, prevErr error) error {
	delay := time.Duration(attempt) * launchRetryBase
	pkglogger.Info("orchestration: retrying launch",
		"agent_id", agentID, "attempt", attempt+1,
		"prev_error", prevErr, "backoff", delay)
	select {
	case <-time.After(delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *service) launchAgentViaLauncher(ctx context.Context, req LaunchRequest) error {
	launched, err := s.launchAgentUntilStarted(ctx, req)
	if err != nil {
		return err
	}
	return s.submitInitialLaunchPrompt(ctx, launched.agentID, launched.result, req)
}

func (s *service) LaunchAgentSnapshot(ctx context.Context, req LaunchRequest) (AgentSnapshot, error) {
	launched, err := s.launchAgentUntilStarted(ctx, req)
	if err != nil {
		return AgentSnapshot{}, err
	}
	snapshot, err := s.Snapshot(ctx, launched.agentID)
	if err != nil {
		return AgentSnapshot{}, err
	}
	s.submitInitialLaunchPromptAsync(launched.agentID, launched.result, req)
	return snapshot, nil
}

func (s *service) launchAgentUntilStarted(ctx context.Context, req LaunchRequest) (launchedAgent, error) {
	attempt, handled, err := s.prepareLauncherLaunch(ctx, req)
	if handled || err != nil {
		return launchedAgent{}, err
	}
	return s.launchWithRetry(ctx, attempt, req)
}

func (s *service) launchWithRetry(ctx context.Context, attempt launcherLaunchAttempt, req LaunchRequest) (launchedAgent, error) {
	var lastErr error
	for i := 0; i < maxLaunchRetries; i++ {
		if i > 0 {
			if err := waitRetryBackoff(ctx, i, attempt.agentID, lastErr); err != nil {
				return launchedAgent{}, s.finishLauncherLaunch(ctx, attempt, LaunchResult{}, err)
			}
		}
		result, launchErr := s.launcher.Launch(ctx, &attempt.launching, req)
		if launchErr == nil {
			if err := s.finishLauncherLaunch(ctx, attempt, result, nil); err != nil {
				return launchedAgent{}, err
			}
			return launchedAgent{agentID: shared.FirstTrimmed(result.RemoteAgentID, attempt.agentID), result: result}, nil
		}
		lastErr = launchErr
		if !isRetryableLaunchError(launchErr) {
			break
		}
	}
	return launchedAgent{}, s.finishLauncherLaunch(ctx, attempt, LaunchResult{}, lastErr)
}

func (s *service) submitInitialLaunchPrompt(ctx context.Context, agentID string, result LaunchResult, req LaunchRequest) error {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		pkglogger.Warn("orchestration: launch prompt auto-submit skipped",
			"agent_id", agentID, "reason", "empty_prompt")
		return nil
	}
	threadID := strings.TrimSpace(result.ThreadID)
	submission := TurnSubmission{
		AgentID:  agentID,
		ThreadID: threadID,
		Inputs: []shareddto.InputItem{{
			Type:    "text",
			Content: prompt,
		}},
	}
	pkglogger.Warn("orchestration: launch prompt auto-submit begin",
		"agent_id", agentID,
		"thread_id", threadID,
		"prompt_len", len([]rune(prompt)))
	if err := s.submitTurnViaLauncher(ctx, submission); err != nil {
		pkglogger.Warn("orchestration: launch prompt auto-submit failed",
			"agent_id", agentID,
			"thread_id", threadID,
			"error", err)
		return err
	}
	pkglogger.Warn("orchestration: launch prompt auto-submit accepted",
		"agent_id", agentID,
		"thread_id", threadID)
	return nil
}

func (s *service) submitInitialLaunchPromptAsync(agentID string, result LaunchResult, req LaunchRequest) {
	if strings.TrimSpace(req.Prompt) == "" {
		return
	}
	go func() {
		bgCtx, cancel := platformconfig.WithTimeout(context.Background(), platformconfig.AsyncLaunchTimeout)
		defer cancel()
		if err := s.submitInitialLaunchPrompt(bgCtx, agentID, result, req); err != nil {
			pkglogger.Warn("orchestration: async launch prompt failed",
				"agent_id", agentID, "error", err)
		}
	}()
}

func (s *service) prepareLauncherLaunch(ctx context.Context, req LaunchRequest) (launcherLaunchAttempt, bool, error) {
	if err := validateLaunchRequest(req); err != nil {
		pkglogger.Warn("orchestration: launch rejected: validation failed",
			"agent_id", req.AgentID, "name", req.Name, "error", err)
		return launcherLaunchAttempt{}, true, err
	}
	s.mu.Lock()
	agent := s.agentForLaunchLocked(req)
	if launchInProgress(ctx, s, agent) {
		pkglogger.Warn("orchestration: launch rejected: already in progress",
			"agent_id", agent.id, "state", agent.state,
			"launch_seq", agent.launchSeq, "last_exited_seq", agent.lastExitedSeq)
		s.mu.Unlock()
		return launcherLaunchAttempt{}, true, fmt.Errorf("agent %q already launched", agent.id)
	}
	if err := s.prepareLaunchLocked(ctx, agent); err != nil {
		pkglogger.Warn("orchestration: launch rejected: prepare failed",
			"agent_id", agent.id, "state", agent.state, "error", err)
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
	agent, err := lookupAgentBySeqLocked(s.agents, attempt.agentID, attempt.expectedSeq)
	if err != nil {
		pkglogger.Warn("orchestration: launch finish: stale seq (agent may have been replaced)",
			"agent_id", attempt.agentID, "expected_seq", attempt.expectedSeq,
			"launch_err", launchErr, "lookup_err", err)
		s.mu.Unlock()
		return s.discardStaleLaunchResult(ctx, &attempt.launching, launchErr)
	}
	if launchErr != nil {
		pkglogger.Warn("orchestration: launch failed",
			"agent_id", attempt.agentID, "state", agent.state,
			"launch_seq", attempt.expectedSeq, "error", launchErr)
		return s.failLauncherLaunchLocked(ctx, agent, &attempt.launching, launchErr)
	}
	return s.completeLauncherLaunchLocked(ctx, agent, &attempt.launching, result)
}

func (s *service) discardStaleLaunchResult(ctx context.Context, launching *agentRuntime, launchErr error) error {
	if launchErr == nil {
		if stopErr := s.launcher.Stop(ctx, launching); stopErr != nil {
			pkglogger.Warn("orchestration: discard stale launch stop failed",
				"agent_id", launching.id, "error", stopErr)
		}
	}
	return launchErr
}

func (s *service) failLauncherLaunchLocked(ctx context.Context, agent, launching *agentRuntime, launchErr error) error {
	err := s.commitLaunchFailureLocked(ctx, agent, launchErr, launching.lastError)
	s.mu.Unlock()
	// Clean up any residual resources on the launching copy (remote thread, etc.).
	if launching != nil && s.launcher != nil {
		if stopErr := s.launcher.Stop(ctx, launching); stopErr != nil {
			pkglogger.Warn("orchestration: fail launch cleanup stop failed",
				"agent_id", launching.id, "error", stopErr)
		}
	}
	return err
}

func (s *service) completeLauncherLaunchLocked(ctx context.Context, agent, launching *agentRuntime, result LaunchResult) error {
	adoptLaunchStateLocked(agent, launching)
	bindLaunchResult(agent, result)
	if err := s.rekeyLaunchedAgentLocked(agent); err != nil {
		commitErr := s.commitLaunchFailureLocked(ctx, agent, err)
		s.mu.Unlock()
		if stopErr := s.launcher.Stop(ctx, launching); stopErr != nil {
			pkglogger.Warn("orchestration: rekey failure cleanup stop failed",
				"agent_id", launching.id, "error", stopErr)
		}
		return commitErr
	}
	if err := s.commitLaunchSuccessLocked(ctx, agent); err != nil {
		agent.cmd = nil
		agent.threadID = ""
		resetRuntimeStateLocked(agent)
		s.mu.Unlock()
		if stopErr := s.launcher.Stop(ctx, launching); stopErr != nil {
			pkglogger.Warn("orchestration: commit success failure cleanup stop failed",
				"agent_id", launching.id, "error", stopErr)
		}
		return err
	}
	s.mu.Unlock()
	return nil
}

func (s *service) rekeyLaunchedAgentLocked(agent *agentRuntime) error {
	if agent == nil {
		return nil
	}
	finalID := strings.TrimSpace(agent.remoteAgentID)
	if finalID == "" || finalID == agent.id {
		return nil
	}
	if existing, ok := s.agents[finalID]; ok && existing != agent {
		return fmt.Errorf("orchestration: remote agent_id %q collides with local agent %q", finalID, existing.id)
	}
	delete(s.agents, agent.id)
	agent.id = finalID
	s.agents[finalID] = agent
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
	s.exitMonitor.Emit(agentID, launchSeq, nil)
	return nil
}

// archiveAgentViaLauncher invokes the remote thread/archive RPC for the
// given agent. Returns (true, nil) when the remote archive flow ran (the
// main app already did UpdateStatus(archived) + SetArchived + cleanup +
// publish). Returns (false, nil) when there is no live runtime and the
// caller should fall back to the persisted DB write path.
func (s *service) archiveAgentViaLauncher(ctx context.Context, agentID, reason string) (bool, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false, errAgentNotFound
	}
	if !s.shouldStopViaLauncher(ctx, agentID) {
		// No live runtime: caller does persisted-only fallback.
		return false, nil
	}
	agent, launchSeq, err := s.prepareLauncherStop(ctx, agentID, reason)
	if err != nil {
		return false, err
	}
	if agent == nil {
		return false, nil
	}
	if err := s.launcher.Archive(ctx, agent); err != nil {
		return false, err
	}
	s.exitMonitor.Emit(agentID, launchSeq, nil)
	return true, nil
}

func (s *service) shouldStopViaLauncher(ctx context.Context, agentID string) bool {
	shouldStop := false
	if err := s.withAgentReadLocked(agentID, func(agent *agentRuntime) error {
		if s.launcher == nil || agent.cmd != nil {
			return nil
		}
		shouldStop = s.launcher.IsRunning(ctx, agent)
		return nil
	}); err != nil {
		pkglogger.Warn("orchestration: shouldStopViaLauncher read failed",
			"agent_id", agentID, "error", err)
	}
	return shouldStop
}

func (s *service) prepareLauncherStop(ctx context.Context, agentID, reason string) (*agentRuntime, uint64, error) {
	var (
		agentRef  *agentRuntime
		launchSeq uint64
	)
	err := s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if !s.agentRunningLocked(ctx, agent) {
			return fmt.Errorf("agent %q is not running", agent.id)
		}
		changed, err := s.markStoppingLocked(ctx, agent, reason)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		agentRef = agent
		launchSeq = agent.launchSeq
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return agentRef, launchSeq, nil
}

func (s *service) submitTurnViaLauncher(ctx context.Context, req TurnSubmission) error {
	agentID, err := submissionAgentID(req)
	if err != nil {
		return err
	}
	s.ensureRuntimeForPersistedAgent(ctx, agentID)
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
	handled := true
	err := s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if !s.canSubmitViaLauncher(ctx, agent) {
			handled = false
			return nil
		}
		if agent.stopRequested {
			return fmt.Errorf("agent %q is stopping", agent.id)
		}
		if remoteAgentBusy(agent) {
			return fmt.Errorf("agent %q is busy", agent.id)
		}
		req.AgentID = agentID
		turnID, err := s.launcher.SubmitTurn(ctx, agent, req)
		if err != nil {
			agent.lastError = err.Error()
			return err
		}
		if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnEnqueued); err != nil {
			return err
		}
		agent.activeTurnID = shared.FirstTrimmed(turnID, req.ExpectedTurnID)
		if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted); err != nil {
			return err
		}
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt, agent.startedAt)
		return nil
	})
	if err != nil {
		return true, err
	}
	return handled, nil
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
	return s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if agent.cmd == nil {
			pkglogger.Warn("orchestration: submit turn rejected: agent not running",
				"agent_id", agent.id, "state", agent.state,
				"launch_seq", agent.launchSeq, "last_exited_seq", agent.lastExitedSeq,
				"last_error", agent.lastError)
			return fmt.Errorf("agent %q is not running", agent.id)
		}
		if agent.stopRequested {
			pkglogger.Warn("orchestration: submit turn rejected: agent stopping",
				"agent_id", agent.id, "state", agent.state, "stop_reason", agent.stopReason)
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
	})
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
	resetLaunchState(dst)
	dst.cmd = src.cmd
	dst.threadID = src.threadID
	dst.remoteThreadID = src.remoteThreadID
	dst.remoteAgentID = src.remoteAgentID
	dst.startedAt = src.startedAt
	dst.updatedAt = src.updatedAt
	dst.exitedAt = shared.CloneTime(src.exitedAt)
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

// isRetryableLaunchError returns true for transient errors that may succeed
// on a subsequent attempt (timeout, connection refused, transport unavailable).
func isRetryableLaunchError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	msg := err.Error()
	for _, pattern := range []string{
		"deadline exceeded",
		"connection refused",
		"transport unavailable",
		"empty thread id",
		"timed out",
		"i/o timeout",
	} {
		if strings.Contains(strings.ToLower(msg), pattern) {
			return true
		}
	}
	return false
}
