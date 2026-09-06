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
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	"go.uber.org/fx"
)

// BindSessionGeneration 绑定线程会话代际，避免旧进程事件误写新会话。
func (s *service) BindSessionGeneration(ctx context.Context, agentID string, generation uint64) error {
	if generation == 0 {
		return errors.New("session generation is required")
	}
	return s.registry.withAgentLocked(agentID, func(agent *agentRuntime) error {
		nextVersion := agent.terminalHeadVersion
		nextUpdatedAt := resolveEventTime(ctx, agent.updatedAt)
		if s.terminalOutcomes != nil {
			threadID := strings.TrimSpace(firstNonEmpty(agent.remoteThreadID, agent.threadID))
			sessionID := agentSessionID(agent)
			if threadID == "" || sessionID == "" {
				return errors.New("terminal outcome session head activation requires thread and session identity")
			}
			var err error
			nextVersion, err = s.resolveTerminalHeadVersion(ctx, agent, threadID, sessionID, generation)
			if err != nil {
				return err
			}
			head, err := s.terminalOutcomes.ActivateTerminalOutcomeHead(ctx, contract.TerminalOutcomeHeadActivation{
				Capability: contract.TerminalOutcomeCapabilityV2, AgentID: strings.TrimSpace(agent.id),
				PublicThreadID: threadID, ProviderTurnID: "session-terminal:" + sessionID,
				SessionID: sessionID, Generation: generation, ExpectedActiveState: string(agent.state),
				ExpectedHeadVersion: nextVersion, ActivatedAt: nextUpdatedAt,
			})
			if err != nil {
				return fmt.Errorf("activate terminal outcome session head: %w", err)
			}
			nextVersion = head.Version
		}
		agent.sessionGeneration = generation
		agent.terminalHeadVersion = nextVersion
		agent.updatedAt = nextUpdatedAt
		return nil
	})
}

// resolveTerminalHeadVersion 从 durable head 恢复 terminal 代际，拒绝身份漂移。
func (s *service) resolveTerminalHeadVersion(
	ctx context.Context,
	agent *agentRuntime,
	threadID, sessionID string,
	generation uint64,
) (uint64, error) {
	if agent.terminalHeadVersion != 0 || s.terminalHeadReader == nil {
		return agent.terminalHeadVersion, nil
	}
	durable, err := s.terminalHeadReader.LoadTerminalOutcomeCurrentHead(ctx, strings.TrimSpace(agent.id))
	if errors.Is(err, contract.ErrTerminalOutcomeHeadNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("rehydrate terminal outcome current head: %w", err)
	}
	if err := validateRehydratedTerminalHead(durable, agent, threadID, sessionID, generation); err != nil {
		return 0, err
	}
	return durable.Version, nil
}

// validateRehydratedTerminalHead 校验恢复的 head 与当前 agent/session/generation 完全一致。
func validateRehydratedTerminalHead(
	head contract.DurableTerminalOutcomeHead,
	agent *agentRuntime,
	threadID, sessionID string,
	generation uint64,
) error {
	if err := head.Validate(); err != nil {
		return fmt.Errorf("rehydrate terminal outcome current head: %w", err)
	}
	if head.Capability != contract.TerminalOutcomeCapabilityV2 ||
		head.AgentID != strings.TrimSpace(agent.id) ||
		head.PublicThreadID != threadID ||
		head.SessionID != sessionID ||
		head.Generation != generation {
		return contract.ErrTerminalOutcomeConflict
	}
	return nil
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

// cleanupSessionAfterTerminalProjection 在 durable outbox ACK 后释放旧 session。
// generation/session id 来自已提交的 terminal identity；只有当前 runtime 仍属于同一
// 生命周期时才清掉它的 generation，避免 ACK 旧终态时误伤新启动的 session。
func (c *lifecycleController) cleanupSessionAfterTerminalProjection(identity contract.CanonicalTerminalIdentity) {
	if c == nil || c.sessionCleaner == nil {
		return
	}
	if strings.TrimSpace(identity.AgentID) == "" || strings.TrimSpace(identity.SessionID) == "" || identity.Generation == 0 {
		return
	}
	c.sessionCleaner.RemoveSessionGeneration(identity.AgentID, identity.Generation)
	if c.registry == nil {
		return
	}
	_ = c.registry.withAgentLocked(identity.AgentID, func(agent *agentRuntime) error {
		if agentSessionID(agent) != identity.SessionID || agent.sessionGeneration != identity.Generation {
			return nil
		}
		agent.sessionGeneration = 0
		return nil
	})
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

// pendingCLIProcessExit 保留启动绑定窗口内的一次严格退出身份。
// 它是 agent runtime 上的单槽，不会随退出通知数量无界增长；事件只有在
// AgentID、LaunchSeq 和 provider SessionID/thread 全部匹配启动结果时才能消费。
type pendingCLIProcessExit struct {
	agentID   string
	sessionID string
	launchSeq uint64
	err       error
}

// handleCLIProcessExit 只接收带完整 session/launch fence 的 standalone 异常退出。
// 明确 Stop/Close 产生的事件不在此重复推进状态机；启动尚未绑定 provider
// thread 时暂存一个精确身份，交由 launch completion 决定是否为本次启动的退出。
func (s *service) handleCLIProcessExit(event contract.CLIProcessExit) {
	if s == nil || s.registry == nil || !event.Unexpected || !event.Valid() {
		return
	}
	agentID, sessionID := strings.TrimSpace(event.AgentID), strings.TrimSpace(event.SessionID)
	if agentID == "" || sessionID == "" {
		return
	}
	event.AgentID, event.SessionID = agentID, sessionID
	s.registry.lock()
	agent, err := s.registry.lookupAgentBySeqLocked(agentID, event.LaunchSeq)
	if err != nil {
		s.registry.unlock()
		return
	}
	if cliProcessExitSessionMatches(agent, sessionID) {
		s.registry.unlock()
		s.handleProcessExit(context.Background(), agentID, event.LaunchSeq, event.Err)
		return
	}
	if canBufferCLIProcessExitLocked(agent, event) {
		bufferCLIProcessExitLocked(agent, event)
	}
	s.registry.unlock()
}

// canBufferCLIProcessExitLocked 仅允许 provisioning/recovering 且尚未绑定 thread
// 的 agent 暂存退出。AgentID 也必须是本次本地启动请求的确切身份。
func canBufferCLIProcessExitLocked(agent *agentRuntime, event contract.CLIProcessExit) bool {
	if agent == nil || !launchOwnsHookThreadBinding(agent.state) {
		return false
	}
	if strings.TrimSpace(firstNonEmpty(agent.remoteThreadID, agent.threadID)) != "" {
		return false
	}
	return cliProcessExitAgentMatches(agent, event.AgentID)
}

// cliProcessExitAgentMatches 限制暂存事件只能来自当前 runtime 的本地或请求身份。
func cliProcessExitAgentMatches(agent *agentRuntime, eventAgentID string) bool {
	if agent == nil {
		return false
	}
	eventAgentID = strings.TrimSpace(eventAgentID)
	return eventAgentID != "" && (eventAgentID == strings.TrimSpace(agent.id) ||
		eventAgentID == strings.TrimSpace(agent.requestedAgentID))
}

// bufferCLIProcessExitLocked 只保留首个身份相同的事件，冲突身份不会覆盖它。
// 调用方必须持有 agentRegistry.mu；该单槽策略同时限制内存和回放范围。
func bufferCLIProcessExitLocked(agent *agentRuntime, event contract.CLIProcessExit) {
	if agent == nil {
		return
	}
	if pending := agent.pendingCLIExit; pending != nil {
		if pending.agentID == event.AgentID && pending.launchSeq == event.LaunchSeq && pending.sessionID == event.SessionID {
			return
		}
		pkglogger.Warn("orchestration: conflicting pending process exit ignored",
			"agent_id", event.AgentID, "launch_seq", event.LaunchSeq,
			"session_id", event.SessionID)
		return
	}
	agent.pendingCLIExit = &pendingCLIProcessExit{
		agentID: event.AgentID, sessionID: event.SessionID,
		launchSeq: event.LaunchSeq, err: event.Err,
	}
}

// takePendingCLIProcessExitLocked 消费启动完成时的单槽退出事件。
// 无论匹配与否都清槽，避免旧身份跨越下一次启动；只有三元身份全等才返回 true。
func takePendingCLIProcessExitLocked(agent *agentRuntime, agentID string, launchSeq uint64, sessionID string) (contract.CLIProcessExit, bool) {
	if agent == nil {
		return contract.CLIProcessExit{}, false
	}
	pending := agent.pendingCLIExit
	agent.pendingCLIExit = nil
	if pending == nil || pending.agentID != strings.TrimSpace(agentID) || pending.launchSeq != launchSeq || pending.sessionID != strings.TrimSpace(sessionID) {
		return contract.CLIProcessExit{}, false
	}
	return contract.CLIProcessExit{
		AgentID: pending.agentID, SessionID: pending.sessionID,
		LaunchSeq: pending.launchSeq, Err: pending.err, Unexpected: true,
	}, true
}

// pendingCLIProcessExitLaunchError 将精确匹配的启动期退出转成启动失败原因。
func pendingCLIProcessExitLaunchError(agentID string, event contract.CLIProcessExit) error {
	cause := event.Err
	if cause == nil {
		cause = errors.New("CLI process exited unexpectedly")
	}
	return fmt.Errorf("agent %q process exited before launch activation (launch_seq=%d, session_id=%q): %w",
		strings.TrimSpace(agentID), event.LaunchSeq, event.SessionID, cause)
}

// cliProcessExitSessionMatches validates the provider session identity. The
// terminal-outcome session generation is the launch sequence rendered as a
// string, but CLIProcessExit.SessionID is the actual provider thread/session
// id (Codex thread id or Claude session UUID); they are distinct fences.
func cliProcessExitSessionMatches(agent *agentState, sessionID string) bool {
	if agent == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	expected := strings.TrimSpace(firstNonEmpty(agent.remoteThreadID, agent.threadID))
	return expected != "" && expected == strings.TrimSpace(sessionID)
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
	c.finalizeProcessExitRuntimeLocked(agent, now, launchSeq, recoverViaLauncher, terminalCommitted)
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
