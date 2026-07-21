package orchestration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/launcherrors"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"

	"go.uber.org/fx"
)

// AgentLaunchSnapshotter 是 DAG launcher 创建 agent snapshot 所需的窄端口。
type AgentLaunchSnapshotter interface {
	launchAgentSnapshot(ctx context.Context, req LaunchRequest, beforeInitialPrompt func(agentID string, result LaunchResult) error) (AgentSnapshot, error)
}

type agentLifecycleController struct {
	launchSnapshots AgentLaunchSnapshotter
	launcher        AgentLauncher
	threads         AgentThreadLookup
	stopper         StopAgentService
}

// AgentLifecycleControllerParams 汇总 DAG agent lifecycle controller 的 fx 端口。
type AgentLifecycleControllerParams struct {
	fx.In

	LaunchSnapshots AgentLaunchSnapshotter
	Launcher        AgentLauncher
	Threads         AgentThreadLookup `optional:"true"`
	Stopper         StopAgentService
}

// ProvideAgentLifecycleController 汇总 DAG agent launcher 所需的窄口。
func ProvideAgentLifecycleController(p AgentLifecycleControllerParams) (*agentLifecycleController, error) {
	if p.LaunchSnapshots == nil {
		return nil, errors.New("agent lifecycle controller: launch snapshotter is nil")
	}
	if p.Stopper == nil {
		return nil, errors.New("agent lifecycle controller: stop service is nil")
	}
	return &agentLifecycleController{
		launchSnapshots: p.LaunchSnapshots,
		launcher:        p.Launcher,
		threads:         p.Threads,
		stopper:         p.Stopper,
	}, nil
}

// launcherLaunchAttempt 保存一次 launcher 启动跨锁执行所需的状态快照和 seq fence。
type launcherLaunchAttempt struct {
	agentID     string
	expectedSeq uint64
	launching   agentRuntime
	forkParent  agentRuntime
}

type lifecycleLaunchPort interface {
	applyLaunchRequestDefaults(ctx context.Context, req LaunchRequest) (LaunchRequest, error)
	prepareLauncherLaunch(ctx context.Context, req LaunchRequest) (launcherLaunchAttempt, bool, error)
	finishLauncherLaunch(ctx context.Context, attempt launcherLaunchAttempt, result LaunchResult, launchErr error) error
	submitInitialLaunchPromptOrStop(ctx context.Context, agentID string, result LaunchResult, req LaunchRequest) error
}

// launchAgentViaLauncher 走 launcher 启动 agent，并在启动成功后提交初始 prompt。
func (s *service) launchAgentViaLauncher(ctx context.Context, req LaunchRequest) error {
	return s.lifecycle.launchAgentViaLauncher(ctx, s, req)
}

func (c *lifecycleController) launchAgentViaLauncher(ctx context.Context, owner lifecycleLaunchPort, req LaunchRequest) error {
	req, err := owner.applyLaunchRequestDefaults(ctx, req)
	if err != nil {
		return err
	}
	agentID, result, err := c.launchAgentUntilStarted(ctx, owner, req)
	if err != nil {
		return err
	}
	return owner.submitInitialLaunchPromptOrStop(ctx, agentID, result, req)
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
	agentID, result, err := s.lifecycle.launchAgentUntilStarted(ctx, s, req)
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
func (c *lifecycleController) launchAgentUntilStarted(ctx context.Context, owner lifecycleLaunchPort, req LaunchRequest) (string, LaunchResult, error) {
	attempt, handled, err := owner.prepareLauncherLaunch(ctx, req)
	if handled || err != nil {
		return "", LaunchResult{}, err
	}
	return c.launchWithRetry(ctx, owner, attempt, req)
}

// launchWithRetry 带退避重试地执行 launcher 启动，失败后由 launcherrors 策略决定是否重试。
func (c *lifecycleController) launchWithRetry(ctx context.Context, owner lifecycleLaunchPort, attempt launcherLaunchAttempt, req LaunchRequest) (string, LaunchResult, error) {
	var lastErr error
	launchStartedAt := time.Now()
	pkglogger.Info("orchestration: launch attempt sequence start", pkglogger.String(pkglogger.FieldAgentID, attempt.agentID), pkglogger.Int64("max_retries", int64(launcherrors.MaxRetries)))
	for i := range launcherrors.MaxRetries {
		if i > 0 {
			if err := launcherrors.WaitRetryBackoff(ctx, i, attempt.agentID, lastErr); err != nil {
				return "", LaunchResult{}, owner.finishLauncherLaunch(ctx, attempt, LaunchResult{}, err)
			}
		}
		attemptStartedAt := time.Now()
		result, launchErr := c.startLauncherAttempt(ctx, &attempt, req)
		if launchErr == nil {
			pkglogger.Info("orchestration: launch attempt succeeded", pkglogger.String(pkglogger.FieldAgentID, attempt.agentID), pkglogger.Int64("attempt", int64(i+1)), pkglogger.Int64(pkglogger.FieldDurationMS, time.Since(attemptStartedAt).Milliseconds()), pkglogger.Int64("total_duration_ms", time.Since(launchStartedAt).Milliseconds()))
			if err := owner.finishLauncherLaunch(ctx, attempt, result, nil); err != nil {
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
	return "", LaunchResult{}, owner.finishLauncherLaunch(ctx, attempt, LaunchResult{}, lastErr)
}

// startLauncherAttempt 根据 context mode 选择普通 launch 或 fork launch。
func (c *lifecycleController) startLauncherAttempt(ctx context.Context, attempt *launcherLaunchAttempt, req LaunchRequest) (LaunchResult, error) {
	if strings.EqualFold(strings.TrimSpace(req.ContextMode), "forked") {
		return c.launcher.Fork(ctx, &attempt.forkParent, &attempt.launching, req)
	}
	return c.launcher.Launch(ctx, &attempt.launching, req)
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
	if err := validateLaunchRequestForLauncher(req, s.lifecycle.launcher); err != nil {
		pkglogger.Warn("orchestration: launch rejected: validation failed", "agent_id", req.AgentID, "name", req.Name, "error", err)
		return launcherLaunchAttempt{}, true, err
	}
	forkParent, err := s.forkParentForLaunch(ctx, req)
	if err != nil {
		return launcherLaunchAttempt{}, true, err
	}
	registry := s.registry
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
	if s.lifecycle.launcher == nil {
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
	registry := s.registry
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
	if s == nil || s.lifecycle == nil || s.lifecycle.agentBindings == nil {
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
	registry := s.registry
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
		if stopErr := s.lifecycle.launcher.Stop(ctx, launching); stopErr != nil {
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
	s.registry.unlock()
	if launching != nil && s.lifecycle.launcher != nil {
		if stopErr := s.lifecycle.launcher.Stop(ctx, launching); stopErr != nil {
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
		s.registry.unlock()
		if stopErr := s.lifecycle.launcher.Stop(ctx, launching); stopErr != nil {
			pkglogger.Warn("orchestration: rekey failure cleanup stop failed", "agent_id", launching.id, "error", stopErr)
		}
		return commitErr
	}
	if err := s.commitLaunchSuccessLocked(ctx, agent); err != nil {
		closeAgentProcessGuard(agent)
		agent.cmd = nil
		agent.threadID = ""
		resetRuntimeStateLocked(agent)
		s.registry.unlock()
		if stopErr := s.lifecycle.launcher.Stop(ctx, launching); stopErr != nil {
			pkglogger.Warn("orchestration: commit success failure cleanup stop failed", "agent_id", launching.id, "error", stopErr)
		}
		return err
	}
	s.registry.unlock()
	return nil
}

// rekeyLaunchedAgentLocked 把 agent 的 map key 从本地生成 ID 改为远端返回的 agentID。
func (s *service) rekeyLaunchedAgentLocked(agent *agentRuntime) error {
	return s.registry.rekeyLaunchedAgentLocked(agent)
}

type lifecycleStopPort interface {
	agentRunningLocked(ctx context.Context, agent *agentRuntime) bool
	markStoppingLocked(ctx context.Context, agent *agentRuntime, reason string) (bool, error)
	stopAgentWithReason(ctx context.Context, agentID, reason string) error
	handleProcessExit(ctx context.Context, agentID string, launchSeq uint64, err error)
}

// stopAgentViaLauncher 通过 launcher 停止 agent 并等待进程退出。
func (s *service) stopAgentViaLauncher(ctx context.Context, agentID, reason string) error {
	return s.lifecycle.stopAgentViaLauncher(ctx, s, s.logger, agentID, reason)
}

// stopAgentViaLauncher 在 lifecycle owner 内选择 launcher 或本地 stop 路径并收口进程退出。
func (c *lifecycleController) stopAgentViaLauncher(ctx context.Context, owner lifecycleStopPort, logger *slog.Logger, agentID, reason string) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return errAgentNotFound
	}
	if !c.shouldStopViaLauncher(ctx, logger, agentID) {
		return owner.stopAgentWithReason(ctx, agentID, reason)
	}
	agent, launchSeq, err := c.prepareLauncherStop(ctx, owner, agentID, reason)
	if err != nil {
		return err
	}
	if agent == nil {
		return nil
	}
	if err := c.launcher.Stop(ctx, agent); err != nil {
		return err
	}
	owner.handleProcessExit(ctx, agentID, launchSeq, nil)
	return nil
}

// archiveAgentViaLauncher 通过 launcher 归档 agent，成功时返回 true。
func (s *service) archiveAgentViaLauncher(ctx context.Context, agentID, reason string) (bool, error) {
	return s.lifecycle.archiveAgentViaLauncher(ctx, s, s.logger, agentID, reason)
}

// archiveAgentViaLauncher 在 lifecycle owner 内执行 launcher archive，并复用进程退出收口路径。
func (c *lifecycleController) archiveAgentViaLauncher(ctx context.Context, owner lifecycleStopPort, logger *slog.Logger, agentID, reason string) (bool, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false, errAgentNotFound
	}
	if !c.shouldStopViaLauncher(ctx, logger, agentID) {
		if c.hasLocalRuntimeAgent(agentID) {
			return false, owner.stopAgentWithReason(ctx, agentID, reason)
		}
		return false, nil
	}
	agent, launchSeq, err := c.prepareLauncherStop(ctx, owner, agentID, reason)
	if err != nil {
		return false, err
	}
	if agent == nil {
		return false, nil
	}
	if err := c.launcher.Archive(ctx, agent); err != nil {
		return false, err
	}
	owner.handleProcessExit(ctx, agentID, launchSeq, nil)
	return true, nil
}

// hasLocalRuntimeAgent 判断 agent 是否仍有本地进程句柄，archive 路径用它区分本地/远端。
func (c *lifecycleController) hasLocalRuntimeAgent(agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false
	}
	hasLocal := false
	_ = c.registry.withAgentReadLocked(agentID, func(agent *agentRuntime) error {
		hasLocal = agent.cmd != nil
		return nil
	})
	return hasLocal
}

// shouldStopViaLauncher 判断 agent 是否由 launcher 管理且当前仍处于运行状态。
func (c *lifecycleController) shouldStopViaLauncher(ctx context.Context, logger *slog.Logger, agentID string) bool {
	shouldStop := false
	if err := c.registry.withAgentReadLocked(agentID, func(agent *agentRuntime) error {
		if c.launcher != nil && agent.cmd == nil {
			shouldStop = c.launcher.IsRunning(ctx, agent)
		}
		return nil
	}); err != nil {
		loggerOrDefault(logger).Warn("orchestration: shouldStopViaLauncher read failed", "agent_id", agentID, "error", err)
	}
	return shouldStop
}

// prepareLauncherStop 在锁内把 agent 标记为 stopping，并返回供 launcher.Stop 使用的快照。
func (c *lifecycleController) prepareLauncherStop(ctx context.Context, owner lifecycleStopPort, agentID, reason string) (*agentRuntime, uint64, error) {
	var (
		agentRef  *agentRuntime
		launchSeq uint64
	)
	err := c.registry.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if !owner.agentRunningLocked(ctx, agent) {
			return fmt.Errorf("%w: agent %q is not running", errAgentNotRunningForStopper, agent.id)
		}
		if _, err := owner.markStoppingLocked(ctx, agent, reason); err != nil {
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
	if s == nil || s.turns == nil {
		return errors.New("turn controller is not configured")
	}
	terminals, err := s.turns.SubmitTurn(ctx, req)
	for _, terminal := range terminals {
		s.handleRemoteTurnTerminal(ctx, terminal)
	}
	return err
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
	if s == nil || s.turns == nil {
		return AgentStateResult{}, errors.New("turn controller is not configured")
	}
	return s.turns.InterruptAgent(ctx, agentID, source)
}

// SubmitTurn 统一处理 turn 提交：远端 launcher 优先，无法远端处理时进入本地队列。
// It returns terminal notifications that arrived before turn/start returned and were reconciled
// against the canonical turn ID.
func (c *turnController) SubmitTurn(ctx context.Context, req TurnSubmission) ([]turndto.TurnTerminalV2, error) {
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		return nil, errors.New("agent id is required")
	}
	if c.rehydrator != nil {
		c.rehydrator.ensureRuntimeForPersistedAgent(ctx, agentID)
	}
	handled, terminals, err := c.trySubmitRemoteTurn(ctx, agentID, req)
	if handled || err != nil {
		return terminals, err
	}
	return nil, c.enqueueLocalTurnSubmission(ctx, agentID, req)
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
func (c *turnController) trySubmitRemoteTurn(ctx context.Context, agentID string, req TurnSubmission) (bool, []turndto.TurnTerminalV2, error) {
	attempt, handled, err := c.prepareRemoteTurnSubmit(ctx, agentID, req)
	if !handled || err != nil {
		return handled, nil, err
	}
	if err := c.beginRemoteTurnSubmit(attempt); err != nil {
		c.finishRemoteTurnSubmitFailure(ctx, attempt, err)
		return true, nil, err
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
		return true, nil, submitErr
	}
	return true, c.finishRemoteTurnSubmitSuccess(ctx, attempt, remoteTurnID), nil
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

// finishRemoteTurnSubmitSuccess 原子绑定 canonical turn ID，并只返回本次 RPC 期间到达的同 ID 终态。
func (c *turnController) finishRemoteTurnSubmitSuccess(ctx context.Context, attempt remoteTurnSubmitAttempt, remoteTurnID string) []turndto.TurnTerminalV2 {
	if c == nil || c.registry == nil {
		return nil
	}
	ref := remoteTurnSubmitRef{agentID: attempt.agentID, provisionalTurnID: attempt.turnID}
	c.remoteTerminalMu.Lock()
	defer c.remoteTerminalMu.Unlock()
	pending := c.takePendingRemoteTerminalsLocked(ref)
	c.registry.lock()
	defer c.registry.unlock()
	agent, err := c.registry.lookupAgentByIDLocked(attempt.agentID)
	if err != nil || strings.TrimSpace(agent.activeTurnID) != attempt.turnID {
		return nil
	}
	canonicalTurnID := shared.FirstTrimmed(remoteTurnID, attempt.turnID)
	agent.activeTurnID = canonicalTurnID
	if agent.state == agentdto.StateTurnStarting {
		if err := c.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted); err != nil {
			pkglogger.Warn("orchestration: failed to mark remote turn running", "agent_id", agent.id, "turn_id", canonicalTurnID, "error", err)
			return nil
		}
	}
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt, agent.startedAt)
	matched := make([]turndto.TurnTerminalV2, 0, len(pending))
	for _, terminal := range pending {
		if strings.TrimSpace(terminal.TurnID) == canonicalTurnID {
			matched = append(matched, terminal)
		}
	}
	return matched
}

// finishRemoteTurnSubmitFailure 将远端提交失败收口为 turn start failure。
func (c *turnController) finishRemoteTurnSubmitFailure(ctx context.Context, attempt remoteTurnSubmitAttempt, submitErr error) {
	c.cancelRemoteTurnSubmit(attempt)
	c.finishTurnStartFailure(ctx, turnWork{agentID: attempt.agentID, turnID: attempt.turnID}, submitErr)
}

type remoteTurnSubmitRef struct {
	agentID           string
	provisionalTurnID string
}

const defaultPendingRemoteTerminalCapacity = 4096

// beginRemoteTurnSubmit opens the bounded reconciliation window before the RPC can emit a push notification.
func (c *turnController) beginRemoteTurnSubmit(attempt remoteTurnSubmitAttempt) error {
	if c == nil {
		return errors.New("turn controller is required")
	}
	ref := remoteTurnSubmitRef{agentID: attempt.agentID, provisionalTurnID: attempt.turnID}
	c.remoteTerminalMu.Lock()
	defer c.remoteTerminalMu.Unlock()
	if c.pendingRemoteTurnSubmits == nil {
		c.pendingRemoteTurnSubmits = make(map[remoteTurnSubmitRef][]turndto.TurnTerminalV2)
	}
	if _, exists := c.pendingRemoteTurnSubmits[ref]; exists {
		return fmt.Errorf("remote turn submit reconciliation already exists for agent %q", attempt.agentID)
	}
	c.pendingRemoteTurnSubmits[ref] = nil
	return nil
}

func (c *turnController) cancelRemoteTurnSubmit(attempt remoteTurnSubmitAttempt) {
	if c == nil {
		return
	}
	ref := remoteTurnSubmitRef{agentID: attempt.agentID, provisionalTurnID: attempt.turnID}
	c.remoteTerminalMu.Lock()
	defer c.remoteTerminalMu.Unlock()
	c.takePendingRemoteTerminalsLocked(ref)
}

func (c *turnController) takePendingRemoteTerminalsLocked(ref remoteTurnSubmitRef) []turndto.TurnTerminalV2 {
	pending := c.pendingRemoteTurnSubmits[ref]
	delete(c.pendingRemoteTurnSubmits, ref)
	c.pendingRemoteTerminalCount -= len(pending)
	return pending
}

func (c *turnController) pendingRemoteTerminalLimitLocked() int {
	if c.pendingRemoteTerminalCapacity > 0 {
		return c.pendingRemoteTerminalCapacity
	}
	return defaultPendingRemoteTerminalCapacity
}

// routeRemoteTurnTerminal 仅在当前 turn 匹配时投递，或在受限启动窗口内暂存终态。
func (c *turnController) routeRemoteTurnTerminal(agentID string, terminal turndto.TurnTerminalV2) (deliver, buffered bool, err error) {
	if c == nil || c.registry == nil {
		return false, false, errors.New("turn controller is not configured")
	}
	c.remoteTerminalMu.Lock()
	defer c.remoteTerminalMu.Unlock()
	c.registry.lock()
	activeTurnID, found := c.registry.remoteTerminalTargetTurnIDLocked(agentID)
	c.registry.unlock()
	if !found {
		return false, false, nil
	}
	canonicalTurnID := strings.TrimSpace(terminal.TurnID)
	if activeTurnID == "" || activeTurnID == canonicalTurnID {
		return true, false, nil
	}
	ref := remoteTurnSubmitRef{agentID: agentID, provisionalTurnID: activeTurnID}
	pending, exists := c.pendingRemoteTurnSubmits[ref]
	if !exists {
		return false, false, nil
	}
	if c.pendingRemoteTerminalCount >= c.pendingRemoteTerminalLimitLocked() {
		return false, false, errors.New("pending remote terminal reconciliation capacity exhausted")
	}
	c.pendingRemoteTurnSubmits[ref] = append(pending, terminal)
	c.pendingRemoteTerminalCount++
	return false, true, nil
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
	if s.lifecycle.launcher != nil {
		return s.lifecycle.launcher.IsRunning(ctx, agent)
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
