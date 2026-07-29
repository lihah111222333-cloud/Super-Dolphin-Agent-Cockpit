package orchestration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/exitmonitor"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/processctl"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	"go.uber.org/fx"
)

// BindSessionGeneration 绑定线程会话代际，避免旧进程事件误写新会话。
func (s *service) BindSessionGeneration(ctx context.Context, agentID string, generation uint64) error {
	if generation == 0 {
		return errors.New("session generation is required")
	}
	return s.registry.withAgentLocked(agentID, func(agent *agentRuntime) error {
		agent.sessionGeneration = generation
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
		return nil
	})
}

// removeSession 清理 agent 绑定的 session generation，并同步重置 runtime 记录。
func (c *lifecycleController) removeSession(agent *agentRuntime) {
	if c == nil || c.sessionCleaner == nil || agent == nil {
		return
	}
	if agent.sessionGeneration == 0 {
		return
	}
	c.sessionCleaner.RemoveSessionGeneration(agent.id, agent.sessionGeneration)
	agent.sessionGeneration = 0
}

// claimTurnWork 从各 agent 队列领取可执行 turn，并在锁内先推进状态。
// 状态推进失败时会把 turn 放回队头，避免任务在并发状态变更中丢失。
func (s *service) claimTurnWork(ctx context.Context) []turnWork {
	if s == nil || s.turns == nil {
		return nil
	}
	return s.turns.claimTurnWork(ctx)
}

// claimTurnWork 从本地队列领取可执行 turn，并先把状态推进到 turn_starting。
func (c *turnController) claimTurnWork(ctx context.Context) []turnWork {
	if c.registry == nil {
		return nil
	}
	c.registry.lock()
	defer c.registry.unlock()

	work := make([]turnWork, 0, len(c.registry.agents))
	for _, agent := range c.registry.agents {
		c.reconcileReadyStateLocked(ctx, agent)
		if !c.agentRunningLocked(ctx, agent) || agent.stopRequested || agent.state != agentdto.StateTurnQueued {
			continue
		}
		submission, ok := agent.queue.Dequeue()
		if !ok {
			continue
		}
		if err := c.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted); err != nil {
			agent.queue.Enqueue(submission)
			c.log().Warn("orchestration: failed to accept queued turn", "agent_id", agent.id, "error", err)
			continue
		}
		turnID := c.turnIDFor(submission)
		submission.ExpectedTurnID = turnID
		if threadID := strings.TrimSpace(submission.ThreadID); threadID != "" {
			agent.threadID = threadID
		}
		agent.activeTurnID = turnID
		agent.providerTurnAlias = providerTurnAlias{}
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
	s.lifecycle.handleProcessExit(ctx, s, s.reports, s.eventBus, s.logger, agentID, launchSeq, err)
}

type processExitReportPort interface {
	setProcessExitFallbackReportLocked(ctx context.Context, agent *agentRuntime, launchSeq uint64, shouldRecover bool)
}

// handleProcessExit 在 lifecycle owner 内处理进程退出围栏、状态迁移、兜底 report 和自动恢复。
func (c *lifecycleController) handleProcessExit(
	ctx context.Context,
	state lifecycleTransitionPort,
	reports processExitReportPort,
	eventBus *event.Dispatcher,
	logger *slog.Logger,
	agentID string,
	launchSeq uint64,
	err error,
) {
	registry := c.registry
	registry.lock()

	agent, lookupErr := registry.lookupAgentBySeqLocked(agentID, launchSeq)
	if lookupErr != nil {
		loggerOrDefault(logger).Warn("orchestration: process exit ignored (stale seq)",
			"agent_id", agentID, "launch_seq", launchSeq, "error", err)
		registry.unlock()
		return
	}
	// exactly-once 围栏：同一 launchSeq 的进程退出一旦处理过，
	// actor 重投、合成退出或测试重复调用都不能再次触发状态迁移。
	if agent.lastExitedSeq >= launchSeq {
		loggerOrDefault(logger).Debug("orchestration: duplicate process exit ignored (seq already drained)",
			"agent_id", agentID, "launch_seq", launchSeq,
			"last_exited_seq", agent.lastExitedSeq)
		registry.unlock()
		return
	}
	stateBefore := agent.state
	shouldRecover := shouldAutoRecoverProcessExitLocked(c.launcher, agent, err)
	recoverViaLauncher := shouldRecover && shouldRecoverViaLauncher(ctx, c.launcher, agent)
	terminalCommitted, lookupErr := commitNonRecoveringProcessExitTerminal(
		ctx, state, agent, launchSeq, err, shouldRecover,
	)
	if lookupErr != nil {
		loggerOrDefault(logger).Warn("orchestration: process exit terminal commit failed",
			"agent_id", agentID, "launch_seq", launchSeq, "error", lookupErr)
		registry.unlock()
		return
	}
	recoverAgentID := agent.id
	now := resolveEventTime(ctx, agent.updatedAt, agent.startedAt)
	closeAgentProcessGuard(agent)
	agent.cmd = nil
	agent.exitedAt = &now
	agent.lastExitedSeq = launchSeq
	agent.updatedAt = now
	resetRuntimeAfterProcessExitLocked(agent, recoverViaLauncher)
	c.removeSession(agent)
	if !terminalCommitted {
		c.recordProcessExitError(eventBus, agent, err)
		c.handleProcessExitTransition(ctx, state, logger, agent)
	}
	loggerOrDefault(logger).Warn("orchestration: agent process exited",
		"agent_id", agentID, "launch_seq", launchSeq,
		"state_before", stateBefore, "state_after", agent.state,
		"stop_requested", agent.stopRequested, "exit_error", err)
	if !terminalCommitted && agent.stopRequested && strings.TrimSpace(agent.stopReason) != "" {
		emitEvent(eventBus, eventTypeAgentStopped, eventAgentID(agent), agent, agent.stopReason)
	}
	if !terminalCommitted {
		reports.setProcessExitFallbackReportLocked(ctx, agent, launchSeq, shouldRecover)
	}
	clearAgentStopReasonLocked(agent)
	registry.unlock()
	c.recovery.recoverAfterProcessExit(ctx, recoverAgentID, launchSeq, shouldRecover)
}

type processExitTerminalCommitter interface {
	CommitProcessExitTerminalLocked(context.Context, *agentRuntime, uint64, error) (bool, error)
}

func commitProcessExitTerminal(
	ctx context.Context,
	state lifecycleTransitionPort,
	agent *agentRuntime,
	launchSeq uint64,
	processErr error,
) (bool, error) {
	committer, ok := state.(processExitTerminalCommitter)
	if !ok {
		return false, nil
	}
	return committer.CommitProcessExitTerminalLocked(ctx, agent, launchSeq, processErr)
}

func commitNonRecoveringProcessExitTerminal(
	ctx context.Context,
	state lifecycleTransitionPort,
	agent *agentRuntime,
	launchSeq uint64,
	processErr error,
	shouldRecover bool,
) (bool, error) {
	if shouldRecover {
		return false, nil
	}
	return commitProcessExitTerminal(ctx, state, agent, launchSeq, processErr)
}

func (c *lifecycleController) recordProcessExitError(eventBus *event.Dispatcher, agent *agentRuntime, err error) {
	if err == nil {
		return
	}
	publicMessage := publicOrchestrationError("Agent process exited unexpectedly.", err)
	agent.lastError = publicMessage
	if !agent.stopRequested {
		emitEvent(eventBus, eventTypeAgentFailed, eventAgentID(agent), agent, publicMessage, true)
	}
}

type lifecycleTransitionPort interface {
	fireOrForceLocked(ctx context.Context, agent *agentRuntime, trigger agentdto.AgentTrigger) error
}

func (c *lifecycleController) handleProcessExitTransition(ctx context.Context, state lifecycleTransitionPort, logger *slog.Logger, agent *agentRuntime) {
	trigger := agentdto.TriggerProcessExited
	message := "orchestration: failed to mark agent failed after process exit"
	if agent.stopRequested {
		message = "orchestration: failed to mark agent stopped after process exit"
	} else if agent.state == agentdto.StateProvisioning || agent.state == agentdto.StateRecovering {
		trigger = agentdto.TriggerLaunchFailed
		message = "orchestration: failed to mark launch failure after process exit"
	}
	if fireErr := state.fireOrForceLocked(ctx, agent, trigger); fireErr != nil {
		loggerOrDefault(logger).Warn(message, "agent_id", agent.id, "error", fireErr)
	}
}

// waitForProcessExit 等待指定 launchSeq 的子进程退出。
// 超时后会尝试强制停止进程，避免 stop 调用无限等待。
func (c *lifecycleController) waitForProcessExit(ctx context.Context, logger *slog.Logger, agentID string, launchSeq uint64) error {
	if launchSeq == 0 {
		return nil
	}
	registry := c.registry
	waitCtx, cancel := platformconfig.WithTimeout(ctx, c.processExitWaitTimeout)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		registry.rLock()
		agent, err := registry.lookupAgentBySeqLocked(agentID, launchSeq)
		exited := err == nil && agent.lastExitedSeq >= launchSeq
		registry.rUnlock()
		if exited {
			return nil
		}
		select {
		case <-waitCtx.Done():
			if ctx != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			return c.forceKillProcess(logger, agentID, launchSeq)
		case <-ticker.C:
		}
	}
}

// forceKillProcess 在温和停止失败后强制结束进程。
func (c *lifecycleController) forceKillProcess(logger *slog.Logger, agentID string, launchSeq uint64) error {
	var (
		cmd   *exec.Cmd
		guard *processctl.Guard
	)
	registry := c.registry
	registry.rLock()
	if agent, err := registry.lookupAgentBySeqLocked(agentID, launchSeq); err == nil &&
		agent.lastExitedSeq < launchSeq && agent.cmd != nil {
		cmd = agent.cmd
		guard = agent.processGuard
	}
	registry.rUnlock()
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return fmt.Errorf("orchestration: timed out waiting for process exit for agent %q; no live process handle", agentID)
	}
	if err := processctl.ForceStop(cmd, guard); err != nil && !errors.Is(err, os.ErrProcessDone) {
		loggerOrDefault(logger).Warn("orchestration: failed to force-kill timed out agent process", "agent_id", agentID, "launch_seq", launchSeq, "error", err)
		return fmt.Errorf("orchestration: failed to force-kill timed out agent process %q: %w", agentID, err)
	}
	loggerOrDefault(logger).Warn("orchestration: timed out waiting for process exit; forced kill issued", "agent_id", agentID, "launch_seq", launchSeq, "timeout", c.processExitWaitTimeout)
	return nil
}

// RunnerRuntimePort 是本地 runner actor 推进 runtime 状态所需的窄端口。
type RunnerRuntimePort interface {
	handleProcessExit(ctx context.Context, agentID string, launchSeq uint64, err error)
	claimTurnWork(ctx context.Context) []turnWork
	startTurnExecution(ctx context.Context, work turnWork)
	publishTurnStalled(agent *agentRuntime, threadID, turnID, reason string, stalledFor time.Duration, timestamp time.Time)
	StopAllAgents(ctx context.Context)
}

// RunnerLifecyclePort 暴露 runner actor 所需的进程生命周期 owner。
type RunnerLifecyclePort interface {
	runnerLifecycleController() *lifecycleController
}

func (s *service) runnerLifecycleController() *lifecycleController {
	if s == nil {
		return nil
	}
	return s.lifecycle
}

type lifecycleStopAllPort interface {
	stopAgentViaLauncher(ctx context.Context, agentID, reason string) error
	DrainAsync(ctx context.Context)
}

func (c *lifecycleController) stopAllAgents(ctx context.Context, stopper lifecycleStopAllPort, logger *slog.Logger) {
	if ctx == nil {
		ctx = context.Background()
	}
	ids := c.registry.agentIDs()
	sort.Strings(ids)
	for _, agentID := range ids {
		if err := stopper.stopAgentViaLauncher(ctx, agentID, "shutdown"); err != nil &&
			!errors.Is(err, errAgentNotFound) {
			loggerOrDefault(logger).Warn("orchestration: failed to stop agent during shutdown", "agent_id", agentID, "error", err)
		}
	}
	stopper.DrainAsync(ctx)
}

func (c *lifecycleController) listAgents() []agentRuntime {
	if c == nil || c.registry == nil {
		return nil
	}
	return c.registry.listAgents()
}

type runnerActor struct {
	logger    *slog.Logger
	lifecycle *lifecycleController
	runtime   RunnerRuntimePort
}

// RunnerActorParams 汇总本地 runner actor 的 fx 注入端口。
type RunnerActorParams struct {
	fx.In

	Logger    *slog.Logger `optional:"true"`
	Lifecycle RunnerLifecyclePort
	Runtime   RunnerRuntimePort
}

// NewRunnerActor 创建本地 runtime 的 runner actor。
func NewRunnerActor(p RunnerActorParams) platformrunner.Runner {
	var lifecycle *lifecycleController
	if p.Lifecycle != nil {
		lifecycle = p.Lifecycle.runnerLifecycleController()
	}
	return &runnerActor{logger: p.Logger, lifecycle: lifecycle, runtime: p.Runtime}
}

const runnerShutdownDrainGrace = 30 * time.Second

func (a *runnerActor) configured() bool {
	return a != nil &&
		a.lifecycle != nil &&
		a.lifecycle.exitMonitor != nil &&
		a.lifecycle.recovery != nil &&
		a.runtime != nil
}

// Run 是本地模式的主循环，负责消费 turn、处理退出事件并检测卡住的 agent。
// remote 模式不注册该 actor，turn 和状态事件由 launcher notify/hooks 回传。
func (a *runnerActor) Run(ctx context.Context) error {
	if !a.configured() {
		return errors.New("runner actor is not configured")
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	stallDetector := &StallDetector{threshold: 30 * time.Second, logger: a.logger}
	exitEvents := a.lifecycle.exitMonitor.ExitEvents()
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
			a.runtime.handleProcessExit(ctx, result.AgentID, result.LaunchSeq, result.Err)
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
	safego.Go(drainCtx, nil, "mcp-orch.runnerActor.stopAll", func(context.Context) {
		defer close(stopDone)
		a.stopAll(drainCtx)
	})
	drainDone := make(chan struct{})
	safego.Go(drainCtx, nil, "mcp-orch.runnerActor.exitMonitorDrain", func(context.Context) {
		defer close(drainDone)
		if err := a.lifecycle.exitMonitor.Drain(drainCtx); err != nil {
			loggerOrDefault(a.logger).Warn("orchestration: exit monitor drain failed",
				slog.String("error", err.Error()),
				slog.Duration("timeout", runnerShutdownDrainGrace),
			)
		}
	})
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
			a.runtime.handleProcessExit(context.Background(), result.AgentID, result.LaunchSeq, result.Err)
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
			a.runtime.handleProcessExit(context.Background(), result.AgentID, result.LaunchSeq, result.Err)
		default:
			return
		}
	}
}

func (a *runnerActor) processTurnQueues(ctx context.Context) {
	for _, work := range a.runtime.claimTurnWork(ctx) {
		a.runtime.startTurnExecution(ctx, work)
	}
}

// recoverStalledAgents 恢复启动中断后卡住的代理状态。
func (a *runnerActor) recoverStalledAgents(ctx context.Context, stallDetector *StallDetector) {
	for _, agent := range a.lifecycle.listAgents() {
		if !stallDetector.CheckStall(&agent) {
			continue
		}
		detectedAt := resolveEventTime(ctx, time.Now())
		stalledFor := time.Duration(0)
		if !agent.updatedAt.IsZero() && detectedAt.After(agent.updatedAt) {
			stalledFor = detectedAt.Sub(agent.updatedAt)
		}
		a.runtime.publishTurnStalled(&agent, agent.threadID, agent.activeTurnID, recoverReasonStall, stalledFor, detectedAt)
		if err := a.lifecycle.recovery.recoverWithReason(ctx, agent.id, recoverReasonStall); err != nil {
			a.logger.Warn("orchestration: stalled agent recovery failed", "agent_id", agent.id, "error", err)
			if notifyErr := a.lifecycle.recovery.notifyRecoveryFailure(ctx, agent.id, err); notifyErr != nil {
				a.logger.Warn("orchestration: stalled recovery failure report notification failed",
					"agent_id", agent.id, "error", notifyErr)
			}
		}
	}
}

func (a *runnerActor) stopAll(ctx context.Context) {
	a.runtime.StopAllAgents(ctx)
}
