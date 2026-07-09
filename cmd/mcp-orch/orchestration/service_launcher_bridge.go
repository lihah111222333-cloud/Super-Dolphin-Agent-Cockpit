package orchestration

import (
	"context"
	"errors"
	"fmt"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/launcherrors"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"strings"
	"time"
)

type agentLaunchSnapshotter interface {
	launchAgentSnapshot(ctx context.Context, req LaunchRequest, beforeInitialPrompt func(agentID string, result LaunchResult) error) (AgentSnapshot, error)
}

type agentLifecycleController struct {
	launchSnapshots agentLaunchSnapshotter
	launcher        AgentLauncher
	threads         AgentThreadLookup
	stopper         StopAgentService
}

// ProvideAgentLifecycleController 将 service 生命周期能力复制到 DAG agent launcher 所需的窄口。
func ProvideAgentLifecycleController(svc *service) (*agentLifecycleController, error) {
	if svc == nil {
		return nil, errors.New("agent lifecycle controller: service is nil")
	}
	return &agentLifecycleController{
		launchSnapshots: svc,
		launcher:        svc.launcher,
		threads:         svc.agentThreads,
		stopper:         svc,
	}, nil
}

// launcherLaunchAttempt 保存一次 launcher 启动跨锁执行所需的状态快照和 seq fence。
type launcherLaunchAttempt struct {
	agentID     string
	expectedSeq uint64
	launching   agentRuntime
	forkParent  agentRuntime
}

// launchAgentViaLauncher 走 launcher 启动 agent，并在启动成功后提交初始 prompt。
func (s *service) launchAgentViaLauncher(ctx context.Context, req LaunchRequest) error {
	req, err := s.applyLaunchRequestDefaults(ctx, req)
	if err != nil {
		return err
	}
	agentID, result, err := s.launchAgentUntilStarted(ctx, req)
	if err != nil {
		return err
	}
	return s.submitInitialLaunchPromptOrStop(ctx, agentID, result, req)
}

// LaunchAgentSnapshot 返回代理启动器当前持有的运行快照。
func (s *service) LaunchAgentSnapshot(ctx context.Context, req LaunchRequest) (AgentSnapshot, error) {
	return s.launchAgentSnapshot(ctx, req, nil)
}

// launchAgentSnapshot 执行完整的 agent 启动流程：参数补全、启动重试、提交初始 prompt，返回快照。
func (s *service) launchAgentSnapshot(ctx context.Context, req LaunchRequest, beforeInitialPrompt func(agentID string, result LaunchResult) error) (AgentSnapshot, error) {
	req, err := s.applyLaunchRequestDefaults(ctx, req)
	if err != nil {
		return AgentSnapshot{}, err
	}
	agentID, result, err := s.launchAgentUntilStarted(ctx, req)
	if err != nil {
		return AgentSnapshot{}, err
	}
	if beforeInitialPrompt != nil {
		if err := beforeInitialPrompt(agentID, result); err != nil {
			return AgentSnapshot{}, s.stopLaunchedAgentAfterBeforePromptFailure(agentID, err)
		}
	}
	if err := s.submitInitialLaunchPromptOrStop(ctx, agentID, result, req); err != nil {
		return AgentSnapshot{}, err
	}
	return s.Snapshot(ctx, agentID)
}

// stopLaunchedAgentAfterBeforePromptFailure 在 beforeInitialPrompt hook 失败后清理刚启动的 agent。
func (s *service) stopLaunchedAgentAfterBeforePromptFailure(agentID string, cause error) error {
	cleanupCtx, cancel := platformconfig.WithTimeout(context.Background(), platformconfig.AsyncLaunchTimeout)
	defer cancel()
	if stopErr := s.stopAgentViaLauncher(cleanupCtx, agentID, "before_initial_prompt_failed"); stopErr != nil {
		return errors.Join(cause, fmt.Errorf("stop launched agent after before-prompt failure: %w", stopErr))
	}
	return cause
}

// applyLaunchRequestDefaults 从父 agent 继承 cwd 等缺省参数。
func (s *service) applyLaunchRequestDefaults(ctx context.Context, req LaunchRequest) (LaunchRequest, error) {
	if req.Cwd != "" || strings.TrimSpace(req.Cwd) != "" || strings.TrimSpace(req.ParentID) == "" {
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

// launchAgentUntilStarted 准备启动 attempt，并按重试策略等待 launcher 返回启动结果。
func (s *service) launchAgentUntilStarted(ctx context.Context, req LaunchRequest) (string, LaunchResult, error) {
	attempt, handled, err := s.prepareLauncherLaunch(ctx, req)
	if handled || err != nil {
		return "", LaunchResult{}, err
	}
	return s.launchWithRetry(ctx, attempt, req)
}

// launchWithRetry 带退避重试地执行 launcher 启动，失败后由 launcherrors 策略决定是否重试。
func (s *service) launchWithRetry(ctx context.Context, attempt launcherLaunchAttempt, req LaunchRequest) (string, LaunchResult, error) {
	var lastErr error
	launchStartedAt := time.Now()
	pkglogger.Info("orchestration: launch attempt sequence start", pkglogger.String(pkglogger.FieldAgentID, attempt.agentID), pkglogger.Int64("max_retries", int64(launcherrors.MaxRetries)))
	for i := range launcherrors.MaxRetries {
		if i > 0 {
			if err := launcherrors.WaitRetryBackoff(ctx, i, attempt.agentID, lastErr); err != nil {
				return "", LaunchResult{}, s.finishLauncherLaunch(ctx, attempt, LaunchResult{}, err)
			}
		}
		attemptStartedAt := time.Now()
		result, launchErr := s.startLauncherAttempt(ctx, &attempt, req)
		if launchErr == nil {
			pkglogger.Info("orchestration: launch attempt succeeded", pkglogger.String(pkglogger.FieldAgentID, attempt.agentID), pkglogger.Int64("attempt", int64(i+1)), pkglogger.Int64(pkglogger.FieldDurationMS, time.Since(attemptStartedAt).Milliseconds()), pkglogger.Int64("total_duration_ms", time.Since(launchStartedAt).Milliseconds()))
			if err := s.finishLauncherLaunch(ctx, attempt, result, nil); err != nil {
				return "", LaunchResult{}, err
			}
			return shared.FirstTrimmed(result.RemoteAgentID, attempt.agentID), result, nil
		}
		lastErr = launchErr
		pkglogger.Warn("orchestration: launch attempt failed", pkglogger.String(pkglogger.FieldAgentID, attempt.agentID), pkglogger.Int64("attempt", int64(i+1)), pkglogger.String(pkglogger.FieldError, launchErr.Error()), pkglogger.String("error_class", string(launcherrors.Classify(launchErr))), pkglogger.Int64(pkglogger.FieldDurationMS, time.Since(attemptStartedAt).Milliseconds()), pkglogger.Int64("total_duration_ms", time.Since(launchStartedAt).Milliseconds()))
		if launcherrors.Classify(launchErr) == launcherrors.ClassPermanent {
			break
		}
	}
	return "", LaunchResult{}, s.finishLauncherLaunch(ctx, attempt, LaunchResult{}, lastErr)
}

// startLauncherAttempt 根据 context mode 选择普通 launch 或 fork launch。
func (s *service) startLauncherAttempt(ctx context.Context, attempt *launcherLaunchAttempt, req LaunchRequest) (LaunchResult, error) {
	if strings.EqualFold(strings.TrimSpace(req.ContextMode), "forked") {
		return s.launcher.Fork(ctx, &attempt.forkParent, &attempt.launching, req)
	}
	return s.launcher.Launch(ctx, &attempt.launching, req)
}

// submitInitialLaunchPrompt 在启动成功后把 launch prompt 自动提交为第一轮 turn。
func (s *service) submitInitialLaunchPrompt(ctx context.Context, agentID string, result LaunchResult, req LaunchRequest) error {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		pkglogger.Warn("orchestration: launch prompt auto-submit skipped", "agent_id", agentID, "reason", "empty_prompt")
		return nil
	}
	threadID := strings.TrimSpace(result.ThreadID)
	submission := TurnSubmission{AgentID: agentID, ThreadID: threadID, Inputs: []shareddto.InputItem{{Type: "text", Content: prompt}}}
	pkglogger.Warn("orchestration: launch prompt auto-submit begin", "agent_id", agentID, "thread_id", threadID, "prompt_len", len([]rune(prompt)))
	if err := s.submitTurnViaLauncher(ctx, submission); err != nil {
		pkglogger.Warn("orchestration: launch prompt auto-submit failed", "agent_id", agentID, "thread_id", threadID, "error", err)
		return err
	}
	pkglogger.Warn("orchestration: launch prompt auto-submit accepted", "agent_id", agentID, "thread_id", threadID)
	return nil
}

// submitInitialLaunchPromptOrStop 在初始 prompt 提交失败时停止新 agent，避免空壳 runtime 留存。
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

// prepareLauncherLaunch 校验请求参数、检测重复启动，并在锁内准备 launch attempt。
func (s *service) prepareLauncherLaunch(ctx context.Context, req LaunchRequest) (launcherLaunchAttempt, bool, error) {
	if err := validateLaunchRequestForLauncher(req, s.launcher); err != nil {
		pkglogger.Warn("orchestration: launch rejected: validation failed", "agent_id", req.AgentID, "name", req.Name, "error", err)
		return launcherLaunchAttempt{}, true, err
	}
	forkParent, err := s.forkParentForLaunch(ctx, req)
	if err != nil {
		return launcherLaunchAttempt{}, true, err
	}
	registry := s.agentRegistry()
	registry.lock()
	defer registry.unlock()
	if existing, err := registry.lookupAgentByIdentityLocked(req.AgentID, agentIdentityLocalOnly); err == nil && launchInProgress(ctx, s, existing) {
		pkglogger.Warn("orchestration: launch rejected: already in progress", "agent_id", existing.id, "state", existing.state, "launch_seq", existing.launchSeq, "last_exited_seq", existing.lastExitedSeq)
		return launcherLaunchAttempt{}, true, fmt.Errorf("agent %q already launched", existing.id)
	}
	if existing := registry.requestedAgentLaunchInProgressLocked(req.AgentID, func(agent *agentRuntime) bool {
		return launchInProgress(ctx, s, agent)
	}); existing != nil {
		return launcherLaunchAttempt{}, true, fmt.Errorf("agent %q already launched", existing.id)
	}
	agent := s.agentForLaunchLocked(req)
	if err := s.prepareLaunchLocked(ctx, agent); err != nil {
		pkglogger.Warn("orchestration: launch rejected: prepare failed", "agent_id", agent.id, "state", agent.state, "error", err)
		return launcherLaunchAttempt{}, true, err
	}
	if s.launcher == nil {
		return launcherLaunchAttempt{}, true, s.startProcessLocked(ctx, agent)
	}
	agent.launchSeq++
	attempt := launcherLaunchAttempt{agentID: agent.id, expectedSeq: agent.launchSeq, launching: *agent, forkParent: forkParent}
	return attempt, false, nil
}

// forkParentForLaunch 在 forked 模式下只从可信 runtime 或持久化绑定解析父线程。
func (s *service) forkParentForLaunch(ctx context.Context, req LaunchRequest) (agentRuntime, error) {
	if !strings.EqualFold(strings.TrimSpace(req.ContextMode), "forked") {
		return agentRuntime{}, nil
	}
	if strings.TrimSpace(req.ParentThreadID) != "" {
		return agentRuntime{}, errors.New("parent_thread_id is not accepted for forked launch; pass parent_id only")
	}
	parentID := strings.TrimSpace(req.ParentID)
	if parentID == "" {
		return agentRuntime{}, errors.New("parent agent id is required for forked launch")
	}
	if parent, ok, err := s.runtimeForkParentForLaunch(parentID); err != nil {
		return agentRuntime{}, err
	} else if ok && strings.TrimSpace(parent.remoteThreadID) != "" {
		return parent, nil
	} else if ok {
		persistedParent, persistedErr := s.persistedForkParentForLaunch(ctx, parentID)
		if persistedErr == nil {
			return persistedParent, nil
		}
		return agentRuntime{}, fmt.Errorf("parent agent %q remote thread id is required for forked launch and trusted persisted binding could not prove ownership: %w", parentID, persistedErr)
	}
	persistedParent, err := s.persistedForkParentForLaunch(ctx, parentID)
	if err != nil {
		return agentRuntime{}, fmt.Errorf("parent agent %q is required for forked launch: %w", parentID, err)
	}
	return persistedParent, nil
}

// runtimeForkParentForLaunch 从当前进程内存态读取可信父 agent 快照。
func (s *service) runtimeForkParentForLaunch(parentID string) (agentRuntime, bool, error) {
	registry := s.agentRegistry()
	registry.rLock()
	defer registry.rUnlock()
	parent, lookupErr := registry.lookupAgentByIdentityLocked(parentID, agentIdentityLocalOnly)
	if lookupErr != nil {
		if errors.Is(lookupErr, errAgentNotFound) {
			return agentRuntime{}, false, nil
		}
		return agentRuntime{}, false, lookupErr
	}
	return *parent, true, nil
}

// persistedForkParentForLaunch 从持久化 binding 和 active thread 证明父 agent 归属并组装 fork 父快照。
func (s *service) persistedForkParentForLaunch(ctx context.Context, parentID string) (agentRuntime, error) {
	if s == nil || s.agentBindings == nil {
		return agentRuntime{}, fmt.Errorf("trusted parent binding for forked launch %q is required", parentID)
	}
	source, reason, err := s.loadPersistedRuntimeSource(ctx, parentID)
	if err != nil {
		return agentRuntime{}, fmt.Errorf("trusted parent binding for forked launch %q lookup failed: %w", parentID, err)
	}
	if reason != "" {
		return agentRuntime{}, fmt.Errorf("trusted parent binding for forked launch %q is not usable: %s", parentID, reason)
	}
	thread, reason, err := s.activePersistedThreadForBinding(ctx, parentID, source.remoteThreadID)
	if err != nil {
		return agentRuntime{}, fmt.Errorf("trusted parent thread for forked launch %q lookup failed: %w", parentID, err)
	}
	if reason != "" {
		return agentRuntime{}, fmt.Errorf("trusted parent thread for forked launch %q is not usable: %s", parentID, reason)
	}
	if thread == nil {
		return agentRuntime{}, fmt.Errorf("trusted parent thread for forked launch %q is missing", parentID)
	}
	now := persistedRuntimeTime(source.binding, thread)
	return agentRuntime{
		id:              parentID,
		name:            persistedRuntimeName(parentID, thread),
		cwd:             persistedRuntimeCWD(source.binding, thread),
		provider:        source.provider,
		providerSource:  "persisted-binding",
		runtimeProvider: source.provider,
		runtimePort:     persistedRuntimePort(thread),
		portSource:      "persisted-thread",
		state:           agentdto.StateIdle,
		threadID:        source.remoteThreadID,
		remoteThreadID:  source.remoteThreadID,
		remoteAgentID:   parentID,
		startedAt:       now,
		updatedAt:       now,
	}, nil
}

// launchInProgress 判断 agent 是否正处于启动或恢复中。
func launchInProgress(ctx context.Context, s *service, agent *agentRuntime) bool {
	if agent == nil || agent.state == agentdto.StateFailed || agent.state == agentdto.StateStopped {
		return false
	}
	if s.agentRunningLocked(ctx, agent) {
		return true
	}
	return agent.launchSeq > agent.lastExitedSeq && (agent.state == agentdto.StateProvisioning || agent.state == agentdto.StateRecovering)
}

// finishLauncherLaunch 在锁内用 launchSeq fence 提交 launcher 启动结果。
func (s *service) finishLauncherLaunch(ctx context.Context, attempt launcherLaunchAttempt, result LaunchResult, launchErr error) error {
	registry := s.agentRegistry()
	registry.lock()
	agent, err := registry.lookupAgentBySeqLocked(attempt.agentID, attempt.expectedSeq)
	if err != nil {
		pkglogger.Warn("orchestration: launch finish: stale seq (agent may have been replaced)", "agent_id", attempt.agentID, "expected_seq", attempt.expectedSeq, "launch_err", launchErr, "lookup_err", err)
		registry.unlock()
		return s.discardStaleLaunchResult(ctx, &attempt.launching, launchErr)
	}
	if launchErr != nil {
		pkglogger.Warn("orchestration: launch failed", "agent_id", attempt.agentID, "state", agent.state, "launch_seq", attempt.expectedSeq, "error", launchErr)
		return s.failLauncherLaunchLocked(ctx, agent, &attempt.launching, launchErr)
	}
	return s.completeLauncherLaunchLocked(ctx, agent, &attempt.launching, result)
}

// discardStaleLaunchResult 停止已过期但实际启动成功的 launcher runtime。
func (s *service) discardStaleLaunchResult(ctx context.Context, launching *agentRuntime, launchErr error) error {
	if launchErr == nil {
		if stopErr := s.launcher.Stop(ctx, launching); stopErr != nil {
			pkglogger.Warn("orchestration: discard stale launch stop failed", "agent_id", launching.id, "error", stopErr)
		}
	}
	return launchErr
}

// failLauncherLaunchLocked 在持锁状态下提交启动失败，并在解锁后清理 launcher runtime。
func (s *service) failLauncherLaunchLocked(ctx context.Context, agent, launching *agentRuntime, launchErr error) error {
	var lastErr string
	if launching != nil {
		lastErr = launching.lastError
	}
	err := s.commitLaunchFailureLocked(ctx, agent, launchErr, lastErr)
	s.agentRegistry().unlock()
	if launching != nil && s.launcher != nil {
		if stopErr := s.launcher.Stop(ctx, launching); stopErr != nil {
			pkglogger.Warn("orchestration: fail launch cleanup stop failed", "agent_id", launching.id, "error", stopErr)
		}
	}
	return err
}

// completeLauncherLaunchLocked 采用 launcher 返回的 runtime 状态并完成 provisioning。
func (s *service) completeLauncherLaunchLocked(ctx context.Context, agent, launching *agentRuntime, result LaunchResult) error {
	adoptLaunchStateLocked(agent, launching)
	bindLaunchResult(agent, result)
	if err := s.rekeyLaunchedAgentLocked(agent); err != nil {
		commitErr := s.commitLaunchFailureLocked(ctx, agent, err)
		s.agentRegistry().unlock()
		if stopErr := s.launcher.Stop(ctx, launching); stopErr != nil {
			pkglogger.Warn("orchestration: rekey failure cleanup stop failed", "agent_id", launching.id, "error", stopErr)
		}
		return commitErr
	}
	if err := s.commitLaunchSuccessLocked(ctx, agent); err != nil {
		closeAgentProcessGuard(agent)
		agent.cmd = nil
		agent.threadID = ""
		resetRuntimeStateLocked(agent)
		s.agentRegistry().unlock()
		if stopErr := s.launcher.Stop(ctx, launching); stopErr != nil {
			pkglogger.Warn("orchestration: commit success failure cleanup stop failed", "agent_id", launching.id, "error", stopErr)
		}
		return err
	}
	s.agentRegistry().unlock()
	return nil
}

// rekeyLaunchedAgentLocked 把 agent 的 map key 从本地生成 ID 改为远端返回的 agentID。
func (s *service) rekeyLaunchedAgentLocked(agent *agentRuntime) error {
	return s.agentRegistry().rekeyLaunchedAgentLocked(agent)
}

// stopAgentViaLauncher 通过 launcher 停止 agent 并等待进程退出。
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

// archiveAgentViaLauncher 通过 launcher 归档 agent，成功时返回 true。
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

// hasLocalRuntimeAgent 判断 agent 是否仍有本地进程句柄，archive 路径用它区分本地/远端。
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

// shouldStopViaLauncher 判断 agent 是否由 launcher 管理且当前仍处于运行状态。
func (s *service) shouldStopViaLauncher(ctx context.Context, agentID string) bool {
	shouldStop := false
	if err := s.withAgentReadLocked(agentID, func(agent *agentRuntime) error {
		if s.launcher != nil && agent.cmd == nil {
			shouldStop = s.launcher.IsRunning(ctx, agent)
		}
		return nil
	}); err != nil {
		pkglogger.Warn("orchestration: shouldStopViaLauncher read failed", "agent_id", agentID, "error", err)
	}
	return shouldStop
}

// prepareLauncherStop 在锁内把 agent 标记为 stopping，并返回供 launcher.Stop 使用的快照。
func (s *service) prepareLauncherStop(ctx context.Context, agentID, reason string) (*agentRuntime, uint64, error) {
	var (
		agentRef  *agentRuntime
		launchSeq uint64
	)
	err := s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if !s.agentRunningLocked(ctx, agent) {
			return fmt.Errorf("%w: agent %q is not running", errAgentNotRunningForStopper, agent.id)
		}
		if _, err := s.markStoppingLocked(ctx, agent, reason); err != nil {
			return err
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

// submitTurnViaLauncher 优先提交到远端 launcher，无法远端处理时回落到本地队列。
func (s *service) submitTurnViaLauncher(ctx context.Context, req TurnSubmission) error {
	return s.turnController().SubmitTurn(ctx, req)
}

// remoteTurnSubmitAttempt 保存远端 turn 提交的 active turn fence 和请求副本。
type remoteTurnSubmitAttempt struct {
	agentID string
	turnID  string
	req     TurnSubmission
	agent   *agentRuntime
}

// InterruptAgent 请求远程 Codex 子 agent 中断当前 turn，并等待状态收口。
func (s *service) InterruptAgent(ctx context.Context, agentID string, source string) (AgentStateResult, error) {
	return s.turnController().InterruptAgent(ctx, agentID, source)
}

// SubmitTurn 统一处理 turn 提交：远端 launcher 优先，无法远端处理时进入本地队列。
func (c *turnController) SubmitTurn(ctx context.Context, req TurnSubmission) error {
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		return errors.New("agent id is required")
	}
	if c.rehydrator != nil {
		c.rehydrator.ensureRuntimeForPersistedAgent(ctx, agentID)
	}
	handled, err := c.trySubmitRemoteTurn(ctx, agentID, req)
	if handled || err != nil {
		return err
	}
	return c.enqueueLocalTurnSubmission(ctx, agentID, req)
}

// InterruptAgent 请求远端 launcher 中断当前 active turn，并轮询等待状态收口。
func (c *turnController) InterruptAgent(ctx context.Context, agentID string, source string) (AgentStateResult, error) {
	source = shared.FirstTrimmed(source, "parent_agent")
	agent, turnID, err := c.prepareInterruptAgent(agentID)
	if err != nil {
		return AgentStateResult{}, err
	}
	if c.launcher == nil {
		return AgentStateResult{}, errors.New("interrupt_agent currently supports remote Codex agents only")
	}
	if err := c.launcher.Interrupt(ctx, &agent, source); err != nil {
		return AgentStateResult{}, err
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, activeTurnID, err := c.interruptAgentSnapshot(agent.id)
		if err != nil {
			return AgentStateResult{}, err
		}
		if activeTurnID == "" && agentStateMatches(agentdto.AgentState(result.State), agentdto.StateIdle, agentdto.StateStopped, agentdto.StateFailed) {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return AgentStateResult{}, fmt.Errorf("timed out waiting for interrupt_agent agent %q active turn %q to settle: %w", agent.id, turnID, ctx.Err())
		case <-ticker.C:
		}
	}
}

// prepareInterruptAgent 校验远端 agent 可中断条件，并复制 launcher.Interrupt 所需快照。
func (c *turnController) prepareInterruptAgent(agentID string) (agentRuntime, string, error) {
	var agent agentRuntime
	turnID := ""
	err := c.withAgentReadLocked(agentID, func(current *agentRuntime) error {
		if c.launcher == nil {
			return errors.New("interrupt_agent currently supports remote Codex agents only")
		}
		if !agentStateMatches(current.state, agentdto.StateTurnRunning, agentdto.StateAwaitingUserInput) {
			return fmt.Errorf("interrupt_agent requires running or awaiting user input agent; agent %q is in state %q", current.id, current.state)
		}
		if turnID = strings.TrimSpace(current.activeTurnID); turnID == "" {
			return fmt.Errorf("interrupt_agent requires active turn for agent %q", current.id)
		}
		if strings.TrimSpace(current.remoteThreadID) == "" {
			return fmt.Errorf("interrupt_agent requires remote thread id for agent %q", current.id)
		}
		agent = *current
		return nil
	})
	return agent, turnID, err
}

// interruptAgentSnapshot 读取中断轮询所需的状态和 active turn。
func (c *turnController) interruptAgentSnapshot(agentID string) (AgentStateResult, string, error) {
	result := AgentStateResult{}
	activeTurnID := ""
	err := c.withAgentReadLocked(agentID, func(current *agentRuntime) error {
		result = AgentStateResult{AgentID: current.id, State: string(current.state)}
		activeTurnID = strings.TrimSpace(current.activeTurnID)
		return nil
	})
	return result, activeTurnID, err
}

// trySubmitRemoteTurn 在 launcher 管理的远端 agent 可用时直接提交 turn。
func (c *turnController) trySubmitRemoteTurn(ctx context.Context, agentID string, req TurnSubmission) (bool, error) {
	attempt, handled, err := c.prepareRemoteTurnSubmit(ctx, agentID, req)
	if !handled || err != nil {
		return handled, err
	}
	remoteTurnID, submitErr := c.launcher.SubmitTurn(ctx, attempt.agent, attempt.req)
	if submitErr != nil {
		c.finishRemoteTurnSubmitFailure(ctx, attempt, submitErr)
		if launcherrors.Classify(submitErr) == launcherrors.ClassPermanent {
			cleanupCtx, cancel := platformconfig.WithTimeout(context.Background(), platformconfig.AsyncLaunchTimeout)
			defer cancel()
			if c.stopper == nil {
				submitErr = errors.Join(submitErr, errors.New("turn stop port is not configured"))
			} else {
				submitErr = errors.Join(submitErr, c.stopper.stopAgentViaLauncher(cleanupCtx, attempt.agentID, "remote_turn_submit_failed"))
			}
		}
		return true, submitErr
	}
	c.finishRemoteTurnSubmitSuccess(ctx, attempt, remoteTurnID)
	return true, nil
}

// prepareRemoteTurnSubmit 校验远端 turn 提交前提并构造提交 attempt。
func (c *turnController) prepareRemoteTurnSubmit(ctx context.Context, agentID string, req TurnSubmission) (remoteTurnSubmitAttempt, bool, error) {
	attempt := remoteTurnSubmitAttempt{}
	handled := true
	err := c.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if !c.canSubmitViaLauncher(ctx, agent) {
			handled = false
			return nil
		}
		if agent.stopRequested {
			return fmt.Errorf("%w: agent %q is stopping", errAgentStoppingForStopper, agent.id)
		}
		if remoteAgentBusy(agent) {
			return fmt.Errorf("agent %q is busy", agent.id)
		}
		req.AgentID = agentID
		req.ExpectedTurnID = c.turnIDFor(req)
		if threadID := strings.TrimSpace(req.ThreadID); threadID != "" {
			agent.threadID = threadID
		}
		if err := c.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnEnqueued); err != nil {
			return err
		}
		agent.activeTurnID = req.ExpectedTurnID
		if err := c.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted); err != nil {
			agent.activeTurnID = ""
			return err
		}
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt, agent.startedAt)
		attempt = remoteTurnSubmitAttempt{agentID: agentID, turnID: req.ExpectedTurnID, req: req, agent: agent}
		return nil
	})
	return attempt, handled, err
}

// finishRemoteTurnSubmitSuccess 将远端返回的 turn id 绑定到 active turn。
func (c *turnController) finishRemoteTurnSubmitSuccess(ctx context.Context, attempt remoteTurnSubmitAttempt, remoteTurnID string) {
	_ = c.withAgentLocked(attempt.agentID, func(agent *agentRuntime) error {
		if agent.activeTurnID != attempt.turnID {
			return nil
		}
		agent.activeTurnID = shared.FirstTrimmed(remoteTurnID, attempt.turnID)
		if agent.state == agentdto.StateTurnStarting {
			if err := c.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted); err != nil {
				return err
			}
		}
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt, agent.startedAt)
		return nil
	})
}

// finishRemoteTurnSubmitFailure 将远端提交失败收口为 turn start failure。
func (c *turnController) finishRemoteTurnSubmitFailure(ctx context.Context, attempt remoteTurnSubmitAttempt, submitErr error) {
	c.finishTurnStartFailure(ctx, turnWork{agentID: attempt.agentID, turnID: attempt.turnID}, submitErr)
}

// canSubmitViaLauncher 判断 agent 是否可通过远端 launcher 接收新 turn。
func (c *turnController) canSubmitViaLauncher(ctx context.Context, agent *agentRuntime) bool {
	return c.launcher != nil && agent.cmd == nil && c.launcher.IsRunning(ctx, agent)
}

// remoteAgentBusy 判断远端 agent 是否仍有未完成 turn。
func remoteAgentBusy(agent *agentRuntime) bool {
	return agent.state != agentdto.StateIdle || agent.activeTurnID != ""
}

// enqueueLocalTurnSubmission 把 turn 放入本地队列等待进程就绪后执行。
func (c *turnController) enqueueLocalTurnSubmission(ctx context.Context, agentID string, req TurnSubmission) error {
	waitForSession, err := c.submitAgentReadyState(ctx, agentID)
	if err != nil {
		return err
	}
	if waitForSession {
		if err := c.waitForSubmitSessionReady(ctx, agentID); err != nil {
			return err
		}
	}
	return c.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if agent.cmd == nil {
			pkglogger.Warn("orchestration: submit turn rejected: agent not running", "agent_id", agent.id, "state", agent.state, "launch_seq", agent.launchSeq, "last_exited_seq", agent.lastExitedSeq, "last_error", agent.lastError)
			return fmt.Errorf("%w: agent %q is not running", errAgentNotRunningForStopper, agent.id)
		}
		if agent.stopRequested {
			pkglogger.Warn("orchestration: submit turn rejected: agent stopping", "agent_id", agent.id, "state", agent.state, "stop_reason", agent.stopReason)
			return fmt.Errorf("%w: agent %q is stopping", errAgentStoppingForStopper, agent.id)
		}
		req.AgentID = agentID
		agent.queue.Enqueue(req)
		if agent.state == agentdto.StateIdle {
			if err := c.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnEnqueued); err != nil {
				return err
			}
		}
		return nil
	})
}

// agentRunningLocked 在持锁上下文中判断本地进程或 launcher runtime 是否仍运行。
func (s *service) agentRunningLocked(ctx context.Context, agent *agentRuntime) bool {
	if agent == nil {
		return false
	}
	if s.launcher != nil {
		return s.launcher.IsRunning(ctx, agent)
	}
	return agent.cmd != nil
}

// adoptLaunchStateLocked 将锁外 launcher 快照采用到当前 agent，调用方必须持有 service 锁。
func adoptLaunchStateLocked(dst, src *agentRuntime) {
	if dst == nil || src == nil {
		return
	}
	resetLaunchState(dst)
	dst.cmd, dst.processGuard, dst.threadID = src.cmd, src.processGuard, src.threadID
	dst.remoteThreadID, dst.remoteAgentID = src.remoteThreadID, src.remoteAgentID
	dst.startedAt, dst.updatedAt, dst.exitedAt = src.startedAt, src.updatedAt, shared.CloneTime(src.exitedAt)
	dst.lastError = src.lastError
}

// bindLaunchResult 将 launcher 返回的 thread/agent id 写入 runtime 状态。
func bindLaunchResult(agent *agentRuntime, result LaunchResult) {
	if agent == nil {
		return
	}
	if threadID := strings.TrimSpace(result.ThreadID); threadID != "" {
		agent.threadID, agent.remoteThreadID = threadID, threadID
	}
	if remoteAgentID := strings.TrimSpace(result.RemoteAgentID); remoteAgentID != "" {
		agent.remoteAgentID = remoteAgentID
	}
}
