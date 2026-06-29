package orchestration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/exitmonitor"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/processctl"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
)

// BindSessionGeneration 绑定线程会话代际，避免旧进程事件误写新会话。
func (s *service) BindSessionGeneration(ctx context.Context, agentID string, generation uint64) error {
	if generation == 0 {
		return errors.New("session generation is required")
	}
	return s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		agent.sessionGeneration = generation
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
		return nil
	})
}

func (s *service) removeSession(agent *agentRuntime) {
	if s.sessionCleaner == nil || agent == nil {
		return
	}
	if agent.sessionGeneration == 0 {
		return
	}
	s.sessionCleaner.RemoveSessionGeneration(agent.id, agent.sessionGeneration)
	agent.sessionGeneration = 0
}

// claimTurnWork 从各 agent 队列领取可执行 turn，并在锁内先推进状态。
// 状态推进失败时会把 turn 放回队头，避免任务在并发状态变更中丢失。
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

// handleProcessExit 把本地 cmd.Wait 结果同步回 agent runtime。
// launchSeq 是进程生命周期围栏：旧进程、重复事件或合成退出只能命中一次；有效退出会清理 active turn，
// 按退出原因推进 stopped/failed，并在终态缺 report 时尝试持久化兜底报告，持久化失败只记日志不重入状态机。
func (s *service) handleProcessExit(ctx context.Context, agentID string, launchSeq uint64, err error) {
	s.mu.Lock()

	agent, lookupErr := lookupAgentBySeqLocked(s.agents, agentID, launchSeq)
	if lookupErr != nil {
		s.logger.Warn("orchestration: process exit ignored (stale seq)",
			"agent_id", agentID, "launch_seq", launchSeq, "error", err)
		s.mu.Unlock()
		return
	}
	// exactly-once 围栏：同一 launchSeq 的进程退出一旦处理过，
	// actor 重投、合成退出或测试重复调用都不能再次触发状态迁移。
	if agent.lastExitedSeq >= launchSeq {
		s.logger.Debug("orchestration: duplicate process exit ignored (seq already drained)",
			"agent_id", agentID, "launch_seq", launchSeq,
			"last_exited_seq", agent.lastExitedSeq)
		s.mu.Unlock()
		return
	}
	stateBefore := agent.state
	shouldRecover := shouldAutoRecoverProcessExitLocked(s, agent, err)
	recoverViaLauncher := shouldRecover && shouldRecoverViaLauncher(ctx, s, agent)
	recoverAgentID := agent.id
	now := resolveEventTime(ctx, agent.updatedAt, agent.startedAt)
	closeAgentProcessGuard(agent)
	agent.cmd = nil
	agent.exitedAt = &now
	agent.lastExitedSeq = launchSeq
	agent.updatedAt = now
	resetRuntimeAfterProcessExitLocked(agent, recoverViaLauncher)
	s.removeSession(agent)
	s.recordProcessExitError(agent, err)
	s.handleProcessExitTransition(ctx, agent)
	s.logger.Warn("orchestration: agent process exited",
		"agent_id", agentID, "launch_seq", launchSeq,
		"state_before", stateBefore, "state_after", agent.state,
		"stop_requested", agent.stopRequested, "exit_error", err)
	if agent.stopRequested && strings.TrimSpace(agent.stopReason) != "" {
		emitEvent(s.eventBus, eventTypeAgentStopped, eventAgentID(agent), agent, agent.stopReason)
	}
	s.setProcessExitFallbackReportLocked(ctx, agent, launchSeq, shouldRecover)
	clearAgentStopReasonLocked(agent)
	s.mu.Unlock()
	s.recoverAfterProcessExit(ctx, recoverAgentID, launchSeq, shouldRecover)
}

func (s *service) recordProcessExitError(agent *agentRuntime, err error) {
	if err == nil {
		return
	}
	agent.lastError = err.Error()
	if !agent.stopRequested {
		emitEvent(s.eventBus, eventTypeAgentFailed, eventAgentID(agent), agent, err.Error(), true)
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

// waitForProcessExit 等待指定 launchSeq 的子进程退出。
// 超时后会尝试强制停止进程，避免 stop 调用无限等待。
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
		agent, err := lookupAgentBySeqLocked(s.agents, agentID, launchSeq)
		exited := err == nil && agent.lastExitedSeq >= launchSeq
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

// forceKillProcess 在温和停止失败后强制结束进程。
func (s *service) forceKillProcess(agentID string, launchSeq uint64) error {
	var (
		cmd   *exec.Cmd
		guard *processctl.Guard
	)
	s.mu.RLock()
	if agent, err := lookupAgentBySeqLocked(s.agents, agentID, launchSeq); err == nil &&
		agent.lastExitedSeq < launchSeq && agent.cmd != nil {
		cmd = agent.cmd
		guard = agent.processGuard
	}
	s.mu.RUnlock()
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return fmt.Errorf("orchestration: timed out waiting for process exit for agent %q; no live process handle", agentID)
	}
	if err := processctl.ForceStop(cmd, guard); err != nil && !errors.Is(err, os.ErrProcessDone) {
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

// NewRunnerActor 创建本地 runtime 的 runner actor。
func NewRunnerActor(logger *slog.Logger, service *service) platformrunner.Runner {
	return &runnerActor{logger: logger, service: service}
}

const runnerShutdownDrainGrace = 30 * time.Second

// Run 是本地模式的主循环，负责消费 turn、处理退出事件并检测卡住的 agent。
// remote 模式不注册该 actor，turn 和状态事件由 launcher notify/hooks 回传。
func (a *runnerActor) Run(ctx context.Context) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	stallDetector := &StallDetector{threshold: 30 * time.Second, logger: a.logger}
	exitEvents := a.service.exitMonitor.ExitEvents()
	for {
		a.processTurnQueues(ctx)

		select {
		case <-ctx.Done():
			a.drainOnStop(exitEvents)
			return ctx.Err()
		case result, ok := <-exitEvents:
			if !ok {
				return ctx.Err()
			}
			a.service.handleProcessExit(ctx, result.AgentID, result.LaunchSeq, result.Err)
		case <-ticker.C:
			a.recoverStalledAgents(ctx, stallDetector)
		}
	}
}

// stop 时先停 agent，再把已经在路上的退出事件处理完。
// 否则 agentRuntime 可能卡在 stopping/provisioning。
func (a *runnerActor) drainOnStop(exitEvents <-chan exitmonitor.Event) {
	drainCtx, cancel := platformconfig.WithTimeout(context.Background(), runnerShutdownDrainGrace)
	defer cancel()
	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		defer func() {
			if r := recover(); r != nil {
				a.service.logger.Error("orchestration: stopAll panic", slog.Any("panic", r))
			}
		}()
		a.stopAll(drainCtx)
	}()
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		defer func() {
			if r := recover(); r != nil {
				a.service.logger.Error("orchestration: drain panic", slog.Any("panic", r))
			}
		}()
		if err := a.service.exitMonitor.Drain(drainCtx); err != nil {
			a.service.logger.Warn("orchestration: exit monitor drain failed",
				slog.String("error", err.Error()),
				slog.Duration("timeout", runnerShutdownDrainGrace),
			)
		}
	}()
	stopped, drained := false, false
	for !stopped || !drained {
		select {
		case <-stopDone:
			stopped = true
			stopDone = nil
		case <-drainDone:
			drained = true
			drainDone = nil
		case result, ok := <-exitEvents:
			if !ok {
				return
			}
			a.service.handleProcessExit(context.Background(), result.AgentID, result.LaunchSeq, result.Err)
		}
	}
	a.flushRemainingExitEvents(exitEvents)
}

// flushRemainingExitEvents 在 actor 退出前清空已到达的退出事件。
// 不阻塞等待新事件，只处理当前缓冲区，避免 shutdown 尾部状态丢失。
func (a *runnerActor) flushRemainingExitEvents(exitEvents <-chan exitmonitor.Event) {
	for {
		select {
		case result, ok := <-exitEvents:
			if !ok {
				return
			}
			a.service.handleProcessExit(context.Background(), result.AgentID, result.LaunchSeq, result.Err)
		default:
			return
		}
	}
}

func (a *runnerActor) processTurnQueues(ctx context.Context) {
	for _, work := range a.service.claimTurnWork(ctx) {
		a.service.startTurnExecution(ctx, work)
	}
}

// recoverStalledAgents 恢复启动中断后卡住的代理状态。
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
			if notifyErr := a.service.notifyRecoveryFailure(ctx, agent.id, err); notifyErr != nil {
				a.logger.Warn("orchestration: stalled recovery failure report notification failed",
					"agent_id", agent.id, "error", notifyErr)
			}
		}
	}
}

func (a *runnerActor) stopAll(ctx context.Context) {
	a.service.StopAllAgents(ctx)
}
