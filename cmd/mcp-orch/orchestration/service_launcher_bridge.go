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
	// rateLimitBackoff is the wait used when the previous launch error looks
	// like an HTTP 429 / rate limit. The launcher uses jrpc2 over a line
	// protocol so we cannot read Retry-After; this fixed default still beats
	// the linear path by ~30x and is consistent with provider docs (60s).
	rateLimitBackoff = 60 * time.Second
)

func computeRetryBackoff(attempt int, prevErr error) time.Duration {
	if isRateLimited(prevErr) {
		return rateLimitBackoff
	}
	return time.Duration(attempt) * launchRetryBase
}

func waitRetryBackoff(ctx context.Context, attempt int, agentID string, prevErr error) error {
	delay := computeRetryBackoff(attempt, prevErr)
	pkglogger.Info("orchestration: retrying launch",
		"agent_id", agentID, "attempt", attempt+1,
		"prev_error", prevErr, "backoff", delay,
		"rate_limited", isRateLimited(prevErr))
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
		// Phase 1.8b: permanent 直接跳出避免烧配额；transient/unknown 继续退避
		// 重试（unknown 视作 transient 是保守选择，避免误杀新型瞬态错误）。
		if classifyLaunchError(launchErr) == launchClassPermanent {
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

// archiveAgentViaLauncher invokes the remote thread/archive RPC for launcher-
// owned agents. Local runtime agents still need the legacy local stop path; once
// that completes the caller falls back to the persisted DB archive write.
// Returns (true, nil) only when the remote archive flow ran (the main app
// already did UpdateStatus(archived) + SetArchived + cleanup + publish).
// Returns (false, nil) when there is no live runtime and the caller should fall
// back to the persisted DB write path.
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
	s.exitMonitor.Emit(agentID, launchSeq, nil)
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
		return true, submitErr
	}
	s.finishRemoteTurnSubmitSuccess(ctx, attempt, remoteTurnID)
	return true, nil
}

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

// launchErrorClass 把 launch error 分成三类，让 launchWithRetry 决定是
// 继续重试 (transient/unknown) 还是直接跳出 (permanent)。值类型为 typed
// string 让 switch 编译期检查并方便日志/前端 reason 字段对齐。
type launchErrorClass string

const (
	launchClassTransient launchErrorClass = "transient"
	launchClassPermanent launchErrorClass = "permanent"
	launchClassUnknown   launchErrorClass = "unknown"
)

// permanentLaunchPatterns 与前端 useAutoContinue.PERMANENT_ERROR_PATTERNS
// 同源（5 类永久错误：401/403/quota/payment/context_length）。命中任一关键字
// 即视为 permanent，launchRetry 直接跳出循环不重试。两层各持一份是因为
// jrpc2 line protocol 让后端不能拿到 HTTP envelope；上游错误在哪一层先撞到
// 就在哪一层先识别。
var permanentLaunchPatterns = []string{
	// permanent_unauthenticated
	"401", "unauthoriz", "invalid api key", "invalid_api_key",
	// permanent_forbidden
	"403", "forbidden", "permission denied",
	// permanent_quota_exhausted
	"quota_exhausted", "insufficient_quota", "usage limit", "out of credits",
	// permanent_payment_required
	"402", "payment_required", "subscription expired",
	// permanent_context_length_exceeded
	"context_length_exceeded", "context length exceeded", "maximum context", "prompt is too long",
}

// transientLaunchPatterns 是已知的瞬态故障关键字（连接级 / 超时 / 启动竞态）。
// 命中则归 transient 让 launchRetry 退避后重试。和 isRateLimited（429）一起
// 构成 transient 集合。
var transientLaunchPatterns = []string{
	"deadline exceeded",
	"connection refused",
	"transport unavailable",
	"empty thread id",
	"timed out",
	"i/o timeout",
}

// classifyLaunchError 把 launch error 分类。permanent 优先级最高（即使消息里
// 同时含 transient 关键字，permanent 仍胜出——例如 "401 timeout"）。
func classifyLaunchError(err error) launchErrorClass {
	if err == nil {
		return launchClassTransient
	}
	msg := strings.ToLower(err.Error())
	for _, pattern := range permanentLaunchPatterns {
		if strings.Contains(msg, pattern) {
			return launchClassPermanent
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return launchClassTransient
	}
	if isRateLimited(err) {
		return launchClassTransient
	}
	for _, pattern := range transientLaunchPatterns {
		if strings.Contains(msg, pattern) {
			return launchClassTransient
		}
	}
	return launchClassUnknown
}

// isRateLimited reports whether err looks like an HTTP 429 / rate limit
// response. We cannot read Retry-After (jrpc2 line protocol strips it), so we
// match common substrings the upstream surfaces in its error message.
func isRateLimited(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, pattern := range []string{
		"rate limit",
		"rate-limit",
		"rate_limit",
		"too many requests",
		"http 429",
		" 429 ",
		"status 429",
		"status: 429",
	} {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}
