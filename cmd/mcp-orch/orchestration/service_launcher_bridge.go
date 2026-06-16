package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/launcherrors"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/eventcore"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
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

// 先让 launcher 启动 agent，再提交首轮 prompt。
// 如果首轮提交失败，要立刻停掉刚启动的 agent。
func (s *service) launchAgentViaLauncher(ctx context.Context, req LaunchRequest) error {
	var err error
	req, err = s.applyLaunchRequestDefaults(ctx, req)
	if err != nil {
		return err
	}
	launched, err := s.launchAgentUntilStarted(ctx, req)
	if err != nil {
		return err
	}
	return s.submitInitialLaunchPromptOrStop(ctx, launched.agentID, launched.result, req)
}

// LaunchAgentSnapshot 返回代理启动器当前持有的运行快照。
func (s *service) LaunchAgentSnapshot(ctx context.Context, req LaunchRequest) (AgentSnapshot, error) {
	return s.launchAgentSnapshot(ctx, req, nil)
}

// launchAgentSnapshot 复制代理启动记录，避免调用方改动内部状态。
func (s *service) launchAgentSnapshot(ctx context.Context, req LaunchRequest, beforeInitialPrompt func(agentID string, result LaunchResult) error) (AgentSnapshot, error) {
	req, err := s.applyLaunchRequestDefaults(ctx, req)
	if err != nil {
		return AgentSnapshot{}, err
	}
	launched, err := s.launchAgentUntilStarted(ctx, req)
	if err != nil {
		return AgentSnapshot{}, err
	}
	if beforeInitialPrompt != nil {
		if err := beforeInitialPrompt(launched.agentID, launched.result); err != nil {
			return AgentSnapshot{}, s.stopLaunchedAgentAfterBeforePromptFailure(launched.agentID, err)
		}
	}
	if err := s.submitInitialLaunchPromptOrStop(ctx, launched.agentID, launched.result, req); err != nil {
		return AgentSnapshot{}, err
	}
	return s.Snapshot(ctx, launched.agentID)
}

func (s *service) stopLaunchedAgentAfterBeforePromptFailure(agentID string, cause error) error {
	cleanupCtx, cancel := platformconfig.WithTimeout(context.Background(), platformconfig.AsyncLaunchTimeout)
	defer cancel()
	if stopErr := s.stopAgentViaLauncher(cleanupCtx, agentID, "before_initial_prompt_failed"); stopErr != nil {
		return errors.Join(cause, fmt.Errorf("stop launched agent after before-prompt failure: %w", stopErr))
	}
	return cause
}

// applyLaunchRequestDefaults 为代理启动请求补齐默认 runtime 和路径。
func (s *service) applyLaunchRequestDefaults(ctx context.Context, req LaunchRequest) (LaunchRequest, error) {
	trimmedCWD := strings.TrimSpace(req.Cwd)
	if req.Cwd != "" || trimmedCWD != "" || strings.TrimSpace(req.ParentID) == "" {
		return req, nil
	}
	snapshot, err := s.Snapshot(ctx, strings.TrimSpace(req.ParentID))
	if err != nil {
		if errors.Is(err, errAgentNotFound) {
			return req, nil
		}
		return LaunchRequest{}, err
	}
	req.Cwd = strings.TrimSpace(snapshot.Cwd)
	return req, nil
}
func (s *service) launchAgentUntilStarted(ctx context.Context, req LaunchRequest) (launchedAgent, error) {
	attempt, handled, err := s.prepareLauncherLaunch(ctx, req)
	if handled || err != nil {
		return launchedAgent{}, err
	}
	return s.launchWithRetry(ctx, attempt, req)
}

// 这里只重试启动，不重试首轮 prompt。
// 启动成功后用 launchSeq 确认结果还属于当前 agent。
func (s *service) launchWithRetry(ctx context.Context, attempt launcherLaunchAttempt, req LaunchRequest) (launchedAgent, error) {
	var lastErr error
	launchStartedAt := time.Now()
	pkglogger.Info("orchestration: launch attempt sequence start",
		pkglogger.String(pkglogger.FieldAgentID, attempt.agentID),
		pkglogger.Int64("max_retries", int64(launcherrors.MaxRetries)))
	for i := 0; i < launcherrors.MaxRetries; i++ {
		if i > 0 {
			if err := launcherrors.WaitRetryBackoff(ctx, i, attempt.agentID, lastErr); err != nil {
				return launchedAgent{}, s.finishLauncherLaunch(ctx, attempt, LaunchResult{}, err)
			}
		}
		attemptStartedAt := time.Now()
		result, launchErr := s.launcher.Launch(ctx, &attempt.launching, req)
		if launchErr == nil {
			pkglogger.Info("orchestration: launch attempt succeeded",
				pkglogger.String(pkglogger.FieldAgentID, attempt.agentID),
				pkglogger.Int64("attempt", int64(i+1)),
				pkglogger.Int64(pkglogger.FieldDurationMS, time.Since(attemptStartedAt).Milliseconds()),
				pkglogger.Int64("total_duration_ms", time.Since(launchStartedAt).Milliseconds()))
			if err := s.finishLauncherLaunch(ctx, attempt, result, nil); err != nil {
				return launchedAgent{}, err
			}
			return launchedAgent{agentID: shared.FirstTrimmed(result.RemoteAgentID, attempt.agentID), result: result}, nil
		}
		lastErr = launchErr
		pkglogger.Warn("orchestration: launch attempt failed",
			pkglogger.String(pkglogger.FieldAgentID, attempt.agentID),
			pkglogger.Int64("attempt", int64(i+1)),
			pkglogger.String(pkglogger.FieldError, launchErr.Error()),
			pkglogger.String("error_class", string(launcherrors.Classify(launchErr))),
			pkglogger.Int64(pkglogger.FieldDurationMS, time.Since(attemptStartedAt).Milliseconds()),
			pkglogger.Int64("total_duration_ms", time.Since(launchStartedAt).Milliseconds()))
		if launcherrors.Classify(launchErr) == launcherrors.ClassPermanent {
			break
		}
	}
	return launchedAgent{}, s.finishLauncherLaunch(ctx, attempt, LaunchResult{}, lastErr)
}

// LaunchRequest.Prompt 是启动后的第一轮 turn。
// 不要把它混进 thread/start 的显示名里。
func (s *service) submitInitialLaunchPrompt(ctx context.Context, agentID string, result LaunchResult, req LaunchRequest) error {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		pkglogger.Warn("orchestration: launch prompt auto-submit skipped", "agent_id", agentID, "reason", "empty_prompt")
		return nil
	}
	threadID := strings.TrimSpace(result.ThreadID)
	submission := TurnSubmission{
		AgentID:  agentID,
		ThreadID: threadID,
		Inputs:   []shareddto.InputItem{{Type: "text", Content: prompt}},
	}
	pkglogger.Warn("orchestration: launch prompt auto-submit begin", "agent_id", agentID, "thread_id", threadID, "prompt_len", len([]rune(prompt)))
	if err := s.submitTurnViaLauncher(ctx, submission); err != nil {
		pkglogger.Warn("orchestration: launch prompt auto-submit failed", "agent_id", agentID, "thread_id", threadID, "error", err)
		return err
	}
	pkglogger.Warn("orchestration: launch prompt auto-submit accepted", "agent_id", agentID, "thread_id", threadID)
	return nil
}
func (s *service) submitInitialLaunchPromptOrStop(ctx context.Context, agentID string, result LaunchResult, req LaunchRequest) error {
	if err := s.submitInitialLaunchPrompt(ctx, agentID, result, req); err != nil {
		cleanupCtx, cancel := platformconfig.WithTimeout(context.Background(), platformconfig.AsyncLaunchTimeout)
		defer cancel()
		if stopErr := s.stopAgentViaLauncher(cleanupCtx, agentID, "initial_prompt_failed"); stopErr != nil {
			return errors.Join(err, fmt.Errorf("stop launched agent after initial prompt failure: %w", stopErr))
		}
		return err
	}
	return nil
}

// 持锁准备 agent，并递增 launchSeq。
// launcher 返回后只能写回同一个 seq，避免旧启动覆盖新 agent。
func (s *service) prepareLauncherLaunch(ctx context.Context, req LaunchRequest) (launcherLaunchAttempt, bool, error) {
	if err := validateLaunchRequestForLauncher(req, s.launcher); err != nil {
		pkglogger.Warn("orchestration: launch rejected: validation failed", "agent_id", req.AgentID, "name", req.Name, "error", err)
		return launcherLaunchAttempt{}, true, err
	}
	s.mu.Lock()
	if existing, err := lookupAgentByIdentityLocked(s.agents, req.AgentID, agentIdentityLocalOnly); err == nil && launchInProgress(ctx, s, existing) {
		pkglogger.Warn("orchestration: launch rejected: already in progress", "agent_id", existing.id, "state", existing.state, "launch_seq", existing.launchSeq, "last_exited_seq", existing.lastExitedSeq)
		s.mu.Unlock()
		return launcherLaunchAttempt{}, true, fmt.Errorf("agent %q already launched", existing.id)
	}
	for _, existing := range s.agents {
		if strings.TrimSpace(existing.requestedAgentID) == req.AgentID && launchInProgress(ctx, s, existing) {
			s.mu.Unlock()
			return launcherLaunchAttempt{}, true, fmt.Errorf("agent %q already launched", existing.id)
		}
	}
	agent := s.agentForLaunchLocked(req)
	if err := s.prepareLaunchLocked(ctx, agent); err != nil {
		pkglogger.Warn("orchestration: launch rejected: prepare failed", "agent_id", agent.id, "state", agent.state, "error", err)
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

// launchInProgress 判断同一启动请求是否仍在处理中。
func launchInProgress(ctx context.Context, s *service, agent *agentRuntime) bool {
	if agent == nil || agent.state == agentdto.StateFailed || agent.state == agentdto.StateStopped {
		return false
	}
	if s.agentRunningLocked(ctx, agent) {
		return true
	}
	return agent.launchSeq > agent.lastExitedSeq &&
		(agent.state == agentdto.StateProvisioning || agent.state == agentdto.StateRecovering)
}

// 写回 launch 结果前先检查 launchSeq。
// 如果 agent 已被替换，成功启动的旧进程也要停掉。
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
	var lastErr string
	if launching != nil {
		lastErr = launching.lastError
	}
	err := s.commitLaunchFailureLocked(ctx, agent, launchErr, lastErr)
	s.mu.Unlock()
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
		closeAgentProcessGuard(agent)
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

// rekeyLaunchedAgentLocked 在锁内更新已启动代理的索引键。
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

// remote/thread 模式停 agent 后，要主动更新本地状态。
// 本地进程仍走 stopAgentWithReason。
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

// archiveAgentViaLauncher 通过启动器归档代理并同步运行记录。
func (s *service) archiveAgentViaLauncher(ctx context.Context, agentID, reason string) (bool, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false, errAgentNotFound
	}
	if !s.shouldStopViaLauncher(ctx, agentID) {
		if s.hasLocalRuntimeAgent(agentID) {
			return false, s.stopAgentWithReason(ctx, agentID, reason)
		}
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
	s.handleProcessExit(ctx, agentID, launchSeq, nil)
	return true, nil
}

func (s *service) hasLocalRuntimeAgent(agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false
	}
	hasLocal := false
	_ = s.withAgentReadLocked(agentID, func(agent *agentRuntime) error {
		hasLocal = agent.cmd != nil
		return nil
	})
	return hasLocal
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
			agentRef = agent
			launchSeq = agent.launchSeq
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

type remoteTurnSubmitAttempt struct {
	agentID string
	turnID  string
	req     TurnSubmission
	agent   *agentRuntime
}

func (s *service) trySubmitRemoteTurn(ctx context.Context, agentID string, req TurnSubmission) (bool, error) {
	attempt, handled, err := s.prepareRemoteTurnSubmit(ctx, agentID, req)
	if !handled || err != nil {
		return handled, err
	}
	remoteTurnID, submitErr := s.launcher.SubmitTurn(ctx, attempt.agent, attempt.req)
	if submitErr != nil {
		s.finishRemoteTurnSubmitFailure(ctx, attempt, submitErr)
		if launcherrors.Classify(submitErr) == launcherrors.ClassPermanent {
			cleanupCtx, cancel := platformconfig.WithTimeout(context.Background(), platformconfig.AsyncLaunchTimeout)
			defer cancel()
			submitErr = errors.Join(submitErr, s.stopAgentViaLauncher(cleanupCtx, attempt.agentID, "remote_turn_submit_failed"))
		}
		return true, submitErr
	}
	s.finishRemoteTurnSubmitSuccess(ctx, attempt, remoteTurnID)
	return true, nil
}

// remote turn/start 前先把 agent 标成 busy。
// 如果 RPC 失败，必须回滚，否则 list_agents 会一直显示忙。
func (s *service) prepareRemoteTurnSubmit(ctx context.Context, agentID string, req TurnSubmission) (remoteTurnSubmitAttempt, bool, error) {
	attempt := remoteTurnSubmitAttempt{}
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
		req.ExpectedTurnID = s.turnIDFor(req)
		if threadID := strings.TrimSpace(req.ThreadID); threadID != "" {
			agent.threadID = threadID
		}
		if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnEnqueued); err != nil {
			return err
		}
		agent.activeTurnID = req.ExpectedTurnID
		if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted); err != nil {
			agent.activeTurnID = ""
			return err
		}
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt, agent.startedAt)
		attempt = remoteTurnSubmitAttempt{agentID: agentID, turnID: req.ExpectedTurnID, req: req, agent: agent}
		return nil
	})
	return attempt, handled, err
}
func (s *service) finishRemoteTurnSubmitSuccess(ctx context.Context, attempt remoteTurnSubmitAttempt, remoteTurnID string) {
	_ = s.withAgentLocked(attempt.agentID, func(agent *agentRuntime) error {
		if agent.activeTurnID != attempt.turnID {
			return nil
		}
		agent.activeTurnID = shared.FirstTrimmed(remoteTurnID, attempt.turnID)
		if agent.state == agentdto.StateTurnStarting {
			if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted); err != nil {
				return err
			}
		}
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt, agent.startedAt)
		return nil
	})
}
func (s *service) finishRemoteTurnSubmitFailure(ctx context.Context, attempt remoteTurnSubmitAttempt, submitErr error) {
	s.finishTurnStartFailure(ctx, turnWork{agentID: attempt.agentID, turnID: attempt.turnID}, submitErr)
}
func (s *service) canSubmitViaLauncher(ctx context.Context, agent *agentRuntime) bool {
	return s.launcher != nil && agent.cmd == nil && s.launcher.IsRunning(ctx, agent)
}
func remoteAgentBusy(agent *agentRuntime) bool {
	return agent.state != agentdto.StateIdle || agent.activeTurnID != ""
}

// enqueueLocalTurnSubmission 把本地 turn 提交放入串行队列。
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
	dst.processGuard = src.processGuard
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
