package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	"github.com/qmuntal/stateless"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/exitmonitor"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/processctl"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformstatemachine "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/statemachine"
)

const submitSessionReadyTimeout = 5 * time.Second

const longWaitLogThreshold = 2 * time.Second

// publicOrchestrationError 将内部错误转换为不含原始原因的公开诊断消息。
func publicOrchestrationError(summary string, cause error) string {
	return providerdto.PublicMessageForError(summary, cause)
}

func buildStatesFromDefinitions(defs []agentdto.TransitionDefinition) []platformstatemachine.StateConfig {
	states := agentdto.StateDefinitions()
	permits := make(map[string][]platformstatemachine.Permit, len(states))
	for _, def := range defs {
		permits[string(def.From)] = append(permits[string(def.From)], platformstatemachine.Permit{
			Trigger: string(def.Trigger),
			Dest:    string(def.To),
		})
	}
	configs := make([]platformstatemachine.StateConfig, 0, len(states))
	for _, def := range states {
		configs = append(configs, platformstatemachine.StateConfig{
			Name:    string(def.Name),
			Permits: permits[string(def.Name)],
		})
	}
	return configs
}

// BindActiveTurnID 把当前活跃 turn 绑定到 provider 返回的真实 turn ID。
func (s *service) BindActiveTurnID(ctx context.Context, agentID, expectedLocalTurnID, providerTurnID string) error {
	if s == nil || s.turns == nil {
		return errors.New("turn controller is not configured")
	}
	return s.turns.BindActiveTurnID(ctx, agentID, expectedLocalTurnID, providerTurnID)
}

// BindActiveTurnID 以 expectedLocalTurnID 为代际围栏，拒绝迟到 provider turn 污染后续本地 turn。
func (c *turnController) BindActiveTurnID(ctx context.Context, agentID, expectedLocalTurnID, providerTurnID string) error {
	expectedLocalTurnID = strings.TrimSpace(expectedLocalTurnID)
	providerTurnID = strings.TrimSpace(providerTurnID)
	if expectedLocalTurnID == "" || providerTurnID == "" {
		return errors.New("expected local turn id and provider turn id are required")
	}
	return c.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if agent.activeTurnID != expectedLocalTurnID {
			return fmt.Errorf("%w: agent %q active turn %q does not match expected local turn %q", errTurnNotActive, agent.id, agent.activeTurnID, expectedLocalTurnID)
		}
		agent.providerTurnAlias = providerTurnAlias{
			localTurnID:    expectedLocalTurnID,
			providerTurnID: providerTurnID,
		}
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
		return nil
	})
}

type providerTurnStartFence struct {
	work              turnWork
	launchSeq         uint64
	sessionGeneration uint64
	agent             agentRuntime
}

// beginProviderTurnStart 在调用 provider 前原子校验 turn ownership，并保存补偿所需的旧生命周期快照。
func (c *turnController) beginProviderTurnStart(work turnWork) (providerTurnStartFence, error) {
	fence := providerTurnStartFence{work: work}
	err := c.withAgentLocked(work.agentID, func(agent *agentRuntime) error {
		if agent.stopRequested {
			return fmt.Errorf("provider turn start rejected: agent %q is stopping", agent.id)
		}
		if agent.activeTurnID != work.turnID || agent.state != agentdto.StateTurnStarting {
			return fmt.Errorf("provider turn start rejected: agent %q no longer owns turn %q in starting state", agent.id, work.turnID)
		}
		fence.launchSeq = agent.launchSeq
		fence.sessionGeneration = agent.sessionGeneration
		fence.agent = *agent
		agent.pendingProviderTurnID = work.turnID
		agent.pendingProviderTerminal = nil
		return nil
	})
	return fence, err
}

func (c *turnController) clearPendingProviderTurnStart(work turnWork) {
	_ = c.withAgentLocked(work.agentID, func(agent *agentRuntime) error {
		if agent.pendingProviderTurnID == work.turnID {
			agent.pendingProviderTurnID = ""
			agent.pendingProviderTerminal = nil
		}
		return nil
	})
}

// deferProviderTurnCompletion 仅将无法立即匹配的 provider 终态暂存到同一正在启动的本地 turn。
// 首个合法终态封存；相同 fingerprint 的重放幂等，冲突终态只记录拒绝证据。
func (c *turnController) deferProviderTurnCompletion(ev turndto.TurnCompleted) bool {
	handled := false
	_ = c.withAgentLocked(ev.AgentID, func(agent *agentRuntime) error {
		if agent.pendingProviderTurnID == "" || agent.pendingProviderTurnID != agent.activeTurnID ||
			!turnCompletedEventMatchesAgentThread(agent, ev.ThreadID) || strings.TrimSpace(ev.TurnID) == "" ||
			strings.TrimSpace(ev.TurnID) == agent.activeTurnID {
			return nil
		}
		if pending := agent.pendingProviderTerminal; pending != nil {
			if providerTurnCompletionFingerprint(*pending) != providerTurnCompletionFingerprint(ev) {
				c.log().Warn("orchestration: rejected conflicting pending provider terminal",
					"agent_id", agent.id, "local_turn_id", agent.pendingProviderTurnID,
					"provider_turn_id", ev.TurnID)
			}
			handled = true
			return nil
		}
		pending := ev
		agent.pendingProviderTerminal = &pending
		handled = true
		return nil
	})
	return handled
}

// providerTurnCompletionFingerprint 为早到 provider 终态的语义字段生成稳定重放判定值。
func providerTurnCompletionFingerprint(ev turndto.TurnCompleted) [sha256.Size]byte {
	payload := fmt.Sprintf("%q|%q|%q|%t|%q|%q|%q|%q|%q|%q|%q|%q|%q",
		ev.AgentID, ev.ThreadID, ev.TurnID, ev.Success, ev.Error, ev.Status, ev.Reason,
		ev.Result, ev.Summary, ev.Message, ev.StopReason, ev.TerminationRequestID, ev.PartialItemIDs)
	return sha256.Sum256([]byte(payload))
}

// reconcileReadyStateLocked 在进程已就绪时修正本地状态和队列状态。
func (c *turnController) reconcileReadyStateLocked(ctx context.Context, agent *agentRuntime) {
	if agent.cmd == nil || agent.stopRequested {
		return
	}
	if agent.activeTurnID == "" && agent.state == agentdto.StateIdle && agent.queue.Len() > 0 {
		if err := c.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnEnqueued); err != nil {
			c.log().Warn("orchestration: failed to mark queued turn", "agent_id", agent.id, "error", err)
		}
		return
	}
	if agent.activeTurnID != "" {
		return
	}
	switch agent.state {
	case agentdto.StateTurnStarting, agentdto.StateTurnRunning:
		if err := c.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnCompleted); err != nil {
			c.log().Warn("orchestration: failed to reconcile ready state", "agent_id", agent.id, "state", agent.state, "error", err)
		}
	}
}

// startTurnExecution 等待 session 可提交后，把排队的 turn 交给 provider 执行。
func (s *service) startTurnExecution(ctx context.Context, work turnWork) {
	if s == nil || s.turns == nil {
		pkglogger.FromContext(ctx).Warn("orchestration: turn controller is not configured", "agent_id", work.agentID, "turn_id", work.turnID)
		return
	}
	s.turns.startTurnExecution(ctx, work)
}

// startTurnExecution 等待 session 可提交后，把已领取的 turn 交给 provider 执行。
func (c *turnController) startTurnExecution(ctx context.Context, work turnWork) {
	startedAt := time.Now()
	logger := pkglogger.FromContext(ctx)
	if c.logger != nil {
		logger = c.logger
	}
	logger.Info("orchestration: turn execution start",
		pkglogger.String(pkglogger.FieldAgentID, work.agentID),
		pkglogger.String(pkglogger.FieldThreadID, work.threadID),
		pkglogger.String(pkglogger.FieldTurnID, work.turnID),
		pkglogger.String(pkglogger.FieldComponent, "submit_turn"))
	if err := c.waitForSubmitSessionReady(ctx, work.agentID); err != nil {
		c.finishTurnStartFailure(ctx, work, err)
		return
	}
	if c.turnStarter == nil {
		c.finishTurnStartFailure(ctx, work, errors.New("turn starter is not configured"))
		return
	}
	fence, err := c.beginProviderTurnStart(work)
	if err != nil {
		logger.Warn("orchestration: provider turn start fence rejected",
			pkglogger.String(pkglogger.FieldAgentID, work.agentID),
			pkglogger.String(pkglogger.FieldThreadID, work.threadID),
			pkglogger.String(pkglogger.FieldTurnID, work.turnID),
			pkglogger.String(pkglogger.FieldError, err.Error()))
		return
	}
	startedTurnID, err := c.turnStarter.StartTurn(ctx, work.submission)
	if err != nil {
		logger.Warn("orchestration: turn execution start failed",
			pkglogger.String(pkglogger.FieldAgentID, work.agentID),
			pkglogger.String(pkglogger.FieldThreadID, work.threadID),
			pkglogger.String(pkglogger.FieldTurnID, work.turnID),
			pkglogger.String(pkglogger.FieldError, err.Error()),
			pkglogger.Int64(pkglogger.FieldDurationMS, time.Since(startedAt).Milliseconds()))
		c.finishTurnStartFailure(ctx, work, err)
		return
	}
	logger.Info("orchestration: turn execution accepted",
		pkglogger.String(pkglogger.FieldAgentID, work.agentID),
		pkglogger.String(pkglogger.FieldThreadID, work.threadID),
		pkglogger.String(pkglogger.FieldTurnID, work.turnID),
		pkglogger.String(pkglogger.FieldComponent, "submit_turn"),
		pkglogger.String("started_turn_id", strings.TrimSpace(startedTurnID)),
		pkglogger.Int64(pkglogger.FieldDurationMS, time.Since(startedAt).Milliseconds()))
	if err := c.finishTurnStartSuccessFenced(ctx, fence, startedTurnID); err != nil {
		compensationErr := c.compensateProviderTurnStart(fence, startedTurnID)
		if compensationErr != nil {
			logger.Error("orchestration: provider turn start fence compensation failed",
				pkglogger.String(pkglogger.FieldAgentID, work.agentID),
				pkglogger.String(pkglogger.FieldThreadID, work.threadID),
				pkglogger.String(pkglogger.FieldTurnID, work.turnID),
				pkglogger.String(pkglogger.FieldError, errors.Join(err, compensationErr).Error()))
			return
		}
		logger.Warn("orchestration: compensated provider turn start after fence loss",
			pkglogger.String(pkglogger.FieldAgentID, work.agentID),
			pkglogger.String(pkglogger.FieldThreadID, work.threadID),
			pkglogger.String(pkglogger.FieldTurnID, work.turnID),
			pkglogger.String(pkglogger.FieldError, err.Error()))
	}
}

// finishTurnStartSuccess 基于当前生命周期快照完成 provider turn 接受处理。
func (c *turnController) finishTurnStartSuccess(ctx context.Context, work turnWork, startedTurnID string) {
	fence := providerTurnStartFence{work: work}
	if err := c.withAgentLocked(work.agentID, func(agent *agentRuntime) error {
		fence.launchSeq = agent.launchSeq
		fence.sessionGeneration = agent.sessionGeneration
		fence.agent = *agent
		return nil
	}); err != nil {
		c.log().Warn("orchestration: capture provider turn start fence failed",
			"agent_id", work.agentID, "turn_id", work.turnID, "error", err)
		return
	}
	if err := c.finishTurnStartSuccessFenced(ctx, fence, startedTurnID); err != nil {
		c.log().Warn("orchestration: finish provider turn start failed",
			"agent_id", work.agentID, "turn_id", work.turnID, "error", err)
	}
}

// finishTurnStartSuccessFenced 在同一锁区校验 start fence、绑定 provider turn，并推进到运行中。
func (c *turnController) finishTurnStartSuccessFenced(ctx context.Context, fence providerTurnStartFence, startedTurnID string) error {
	work := fence.work
	currentTurnID := strings.TrimSpace(startedTurnID)
	if currentTurnID == "" {
		currentTurnID = work.turnID
	}
	var pendingTerminal *turndto.TurnCompleted
	if err := c.withAgentLocked(work.agentID, func(agent *agentRuntime) error {
		var err error
		pendingTerminal, err = c.finishTurnStartSuccessLocked(ctx, agent, fence, currentTurnID)
		return err
	}); err != nil {
		return err
	}
	if pendingTerminal != nil && c.terminalSink != nil {
		c.terminalSink.handleBufferedProviderTurnCompletion(ctx, *pendingTerminal)
	}
	return nil
}

// finishTurnStartSuccessLocked 原子完成 fence 校验、别名绑定、终态提取与状态推进。
func (c *turnController) finishTurnStartSuccessLocked(
	ctx context.Context,
	agent *agentRuntime,
	fence providerTurnStartFence,
	currentTurnID string,
) (*turndto.TurnCompleted, error) {
	work := fence.work
	if !providerTurnStartFenceMatchesLocked(agent, fence) {
		clearProviderTurnStartFenceLocked(agent, fence)
		return nil, fmt.Errorf("provider turn start fence lost for agent %q turn %q", work.agentID, work.turnID)
	}
	head, err := c.activateTerminalTurnHeadLocked(ctx, agent, currentTurnID, string(agentdto.StateTurnRunning))
	if err != nil {
		return nil, err
	}
	if currentTurnID != work.turnID {
		agent.providerTurnAlias = providerTurnAlias{localTurnID: work.turnID, providerTurnID: currentTurnID}
	}
	pendingTerminal := matchingPendingProviderTerminal(agent.pendingProviderTerminal, currentTurnID)
	agent.pendingProviderTurnID = ""
	agent.pendingProviderTerminal = nil
	if err := c.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted); err != nil {
		return nil, fmt.Errorf("mark provider turn %q running: %w", currentTurnID, err)
	}
	agent.terminalHeadVersion = head.Version
	return pendingTerminal, nil
}

// activateTerminalTurnHeadLocked 在 runtime 可见突变前建立真实 CAS head。
func (c *turnController) activateTerminalTurnHeadLocked(ctx context.Context, agent *agentRuntime, providerTurnID, expectedState string) (contract.TerminalOutcomeHead, error) {
	if c == nil || c.terminalOutcomes == nil {
		version := uint64(0)
		if agent != nil {
			version = agent.terminalHeadVersion
		}
		return contract.TerminalOutcomeHead{Version: version}, nil
	}
	if agent == nil {
		return contract.TerminalOutcomeHead{}, errAgentNotFound
	}
	threadID := strings.TrimSpace(firstNonEmpty(agent.remoteThreadID, agent.threadID))
	sessionID := agentSessionID(agent)
	if threadID == "" || strings.TrimSpace(providerTurnID) == "" || sessionID == "" || agent.sessionGeneration == 0 {
		return contract.TerminalOutcomeHead{}, errors.New("terminal outcome v2 head activation requires thread, turn, session and generation")
	}
	head, err := c.terminalOutcomes.ActivateTerminalOutcomeHead(ctx, contract.TerminalOutcomeHeadActivation{
		Capability: contract.TerminalOutcomeCapabilityV2, AgentID: strings.TrimSpace(agent.id),
		PublicThreadID: threadID, ProviderTurnID: strings.TrimSpace(providerTurnID),
		SessionID: sessionID, Generation: agent.sessionGeneration,
		ExpectedActiveState: strings.TrimSpace(expectedState), ExpectedHeadVersion: agent.terminalHeadVersion,
		ActivatedAt: resolveEventTime(ctx, agent.updatedAt, agent.startedAt),
	})
	if err != nil {
		return contract.TerminalOutcomeHead{}, fmt.Errorf("activate terminal outcome current head: %w", err)
	}
	return head, nil
}

// clearProviderTurnStartFenceLocked 只清理旧生命周期自身建立的 pending fence。
func clearProviderTurnStartFenceLocked(agent *agentRuntime, fence providerTurnStartFence) {
	if agent.pendingProviderTurnID != fence.work.turnID {
		return
	}
	sameLifecycle := agent.launchSeq == fence.launchSeq &&
		agent.sessionGeneration == fence.sessionGeneration &&
		agent.remoteThreadID == fence.agent.remoteThreadID &&
		agent.cmd == fence.agent.cmd
	if !sameLifecycle && agent.activeTurnID == fence.work.turnID {
		return
	}
	agent.pendingProviderTurnID = ""
	agent.pendingProviderTerminal = nil
}

// matchingPendingProviderTerminal 返回与 provider turn 精确匹配的提前终态。
func matchingPendingProviderTerminal(pending *turndto.TurnCompleted, providerTurnID string) *turndto.TurnCompleted {
	if pending == nil || strings.TrimSpace(pending.TurnID) != providerTurnID {
		return nil
	}
	return pending
}

// providerTurnStartFenceMatchesLocked 校验 provider 返回时仍属于发起请求的同一生命周期。
func providerTurnStartFenceMatchesLocked(agent *agentRuntime, fence providerTurnStartFence) bool {
	return agent != nil && !agent.stopRequested &&
		agent.state == agentdto.StateTurnStarting && agent.activeTurnID == fence.work.turnID &&
		agent.pendingProviderTurnID == fence.work.turnID &&
		agent.launchSeq == fence.launchSeq && agent.sessionGeneration == fence.sessionGeneration &&
		agent.remoteThreadID == fence.agent.remoteThreadID && agent.cmd == fence.agent.cmd
}

// compensateProviderTurnStart 对旧生命周期快照执行有界中断，并在中断失败时停止旧 session/process。
func (c *turnController) compensateProviderTurnStart(fence providerTurnStartFence, startedTurnID string) error {
	snapshot := fence.agent
	snapshot.activeTurnID = strings.TrimSpace(startedTurnID)
	if snapshot.activeTurnID == "" {
		snapshot.activeTurnID = fence.work.turnID
	}
	if c.launcher == nil {
		if snapshot.cmd == nil {
			return errors.New("provider turn start compensation has no launcher or process snapshot")
		}
		return processctl.RequestStop(snapshot.cmd, snapshot.processGuard)
	}
	interruptCtx, cancelInterrupt := platformconfig.WithTimeout(context.Background(), platformconfig.InterruptSettleTimeout)
	interruptErr := c.launcher.Interrupt(interruptCtx, &snapshot, "provider_start_fence_lost")
	cancelInterrupt()
	if interruptErr == nil {
		return nil
	}
	c.log().Warn("orchestration: provider turn start compensation interrupt failed; stopping old lifecycle",
		"agent_id", fence.work.agentID, "turn_id", snapshot.activeTurnID, "error", interruptErr)
	stopCtx, cancelStop := platformconfig.WithTimeout(context.Background(), platformconfig.InterruptSettleTimeout)
	stopErr := c.launcher.Stop(stopCtx, &snapshot)
	cancelStop()
	if stopErr != nil {
		return errors.Join(fmt.Errorf("interrupt late provider turn: %w", interruptErr), fmt.Errorf("stop old provider lifecycle: %w", stopErr))
	}
	return nil
}

func (c *turnController) finishTurnStartFailure(ctx context.Context, work turnWork, startErr error) {
	c.clearPendingProviderTurnStart(work)
	if lockErr := c.withAgentLocked(work.agentID, func(agent *agentRuntime) error {
		if agent.activeTurnID != work.turnID {
			return nil
		}
		agent.lastError = publicOrchestrationError("Agent turn failed to start.", startErr)
		agent.activeTurnID = ""
		agent.providerTurnAlias = providerTurnAlias{}
		if agent.state != agentdto.StateTurnStarting {
			return nil
		}
		if err := c.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnCompleted); err != nil {
			c.log().Warn("orchestration: failed to reset turn after start error", "agent_id", agent.id, "turn_id", work.turnID, "error", err)
		}
		return nil
	}); lockErr != nil {
		c.log().Warn("orchestration: finish turn start failure lock failed",
			"agent_id", work.agentID, "turn_id", work.turnID, "error", lockErr)
	}
}

type lifecycleStopStatePort interface {
	markStoppingLocked(ctx context.Context, agent *agentRuntime, reason string) (bool, error)
}

func (c *lifecycleController) stopAgentLocked(ctx context.Context, state lifecycleStopStatePort, agent *agentRuntime, reason string) error {
	changed, err := state.markStoppingLocked(ctx, agent, reason)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return processctl.RequestStop(agent.cmd, agent.processGuard)
}

func (s *service) stopAgentWithReason(ctx context.Context, agentID, reason string) error {
	launchSeq, err := s.lifecycle.requestAgentStop(ctx, agentID, reason, s)
	if err != nil {
		return err
	}
	return s.lifecycle.waitForProcessExit(ctx, s.logger, strings.TrimSpace(agentID), launchSeq)
}

func (c *lifecycleController) requestAgentStop(ctx context.Context, agentID, reason string, state lifecycleStopStatePort) (uint64, error) {
	launchSeq := uint64(0)
	err := c.registry.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if agent.cmd != nil {
			launchSeq = agent.launchSeq
		}
		return c.stopAgentLocked(ctx, state, agent, reason)
	})
	return launchSeq, err
}

func setStopReasonIfEmpty(agent *agentRuntime, reason string) {
	if agent == nil || strings.TrimSpace(agent.stopReason) != "" {
		return
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		agent.stopReason = reason
	}
}

func (c *turnController) submitAgentReadyState(ctx context.Context, agentID string) (bool, error) {
	ready := false
	err := c.withAgentReadLocked(agentID, func(agent *agentRuntime) error {
		if !c.agentRunningLocked(ctx, agent) {
			return fmt.Errorf("%w: agent %q is not running", errAgentNotRunningForStopper, agent.id)
		}
		if agent.stopRequested {
			return fmt.Errorf("%w: agent %q is stopping", errAgentStoppingForStopper, agent.id)
		}
		ready = agent.state == agentdto.StateIdle && agent.activeTurnID == "" && agent.queue.Len() == 0
		return nil
	})
	return ready, err
}

// waitForSubmitSessionReady 在提交 turn 前等待 provider session 完成启动。
func (s *service) waitForSubmitSessionReady(ctx context.Context, agentID string) error {
	if s == nil || s.turns == nil {
		return errors.New("turn controller is not configured")
	}
	return s.turns.waitForSubmitSessionReady(ctx, agentID)
}

// waitForSubmitSessionReady 在提交 turn 前等待 provider session 完成启动。
func (c *turnController) waitForSubmitSessionReady(ctx context.Context, agentID string) error {
	if c == nil || c.turnStarter == nil {
		return errors.New("turn starter is not configured")
	}
	startedAt := time.Now()
	logger := pkglogger.FromContext(ctx)
	if c.logger != nil {
		logger = c.logger
	}
	logger.Info("orchestration: waiting for submit session ready",
		pkglogger.String(pkglogger.FieldAgentID, agentID),
		pkglogger.String(pkglogger.FieldComponent, "submit_turn"),
		pkglogger.Int64(pkglogger.FieldDurationMS, submitSessionReadyTimeout.Milliseconds()))
	err := c.turnStarter.WaitForSessionReady(ctx, agentID, submitSessionReadyTimeout)
	elapsed := time.Since(startedAt)
	attrs := []any{
		pkglogger.String(pkglogger.FieldAgentID, agentID),
		pkglogger.String(pkglogger.FieldComponent, "submit_turn"),
		pkglogger.Int64(pkglogger.FieldDurationMS, elapsed.Milliseconds()),
	}
	if err != nil {
		attrs = append(attrs, pkglogger.String(pkglogger.FieldError, err.Error()))
		logger.Warn("orchestration: submit session ready wait failed", attrs...)
		return err
	}
	if elapsed >= longWaitLogThreshold {
		logger.Warn("orchestration: submit session ready wait slow", attrs...)
	} else {
		logger.Info("orchestration: submit session ready wait completed", attrs...)
	}
	return nil
}

// startProcessLocked 启动本地 agent 进程，并在启动成功后立即接入退出监控。
func (s *service) startProcessLocked(ctx context.Context, agent *agentRuntime) error {
	nextSeq := agent.launchSeq + 1
	cmd, guard, err := exitmonitor.StartMonitoredCommand(
		s.lifecycle.exitMonitor, s.logger,
		exitmonitor.Target{AgentID: agent.id, LaunchSeq: nextSeq},
		agent.command, agent.cwd,
		append(contract.ScrubDatabaseEnv(os.Environ()), contract.ScrubDatabaseEnv(agent.env)...),
	)
	if err != nil {
		return s.commitLaunchFailureLocked(ctx, agent, err)
	}
	now := resolveEventTime(ctx, agent.updatedAt)
	resetLaunchState(agent)
	agent.cmd, agent.processGuard = cmd, guard
	agent.launchSeq, agent.startedAt, agent.updatedAt = nextSeq, now, now
	if err := s.commitLaunchSuccessLocked(ctx, agent); err != nil {
		if stopErr := processctl.ForceStop(cmd, guard); stopErr != nil {
			s.logger.Warn("orchestration: rollback stop process failed",
				"agent_id", agent.id, "error", stopErr)
		}
		if guard != nil {
			guard.Close()
		}
		agent.cmd, agent.processGuard = nil, nil
		return err
	}
	agent.monitoredSeq = nextSeq
	s.logger.Info("orchestration: agent launched", "agent_id", agent.id, "pid", cmd.Process.Pid)
	return nil
}

// fireOrForceLocked 在持锁状态下触发状态机。
// 非法迁移会带上当前状态、触发器和允许触发器列表，便于调用方快速定位卡住路径。
func (s *service) fireOrForceLocked(ctx context.Context, agent *agentRuntime, trigger agentdto.AgentTrigger) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var before string
	if agent != nil {
		before = string(agent.state)
	}
	if agent == nil || agent.sm == nil {
		return formatIllegalTransitionError(ctx, agent, before, string(trigger), errors.New("state machine is not initialized"))
	}
	if err := s.fireAndPublishLocked(ctx, agent, trigger); err != nil {
		return formatIllegalTransitionError(ctx, agent, before, string(trigger), err)
	}
	return nil
}

func formatIllegalTransitionError(ctx context.Context, agent *agentRuntime, before, trigger string, err error) error {
	allowed, allowedErr := allowedTriggersForState(ctx, agent, before)
	if allowedErr != nil {
		err = fmt.Errorf("failed to retrieve allowed triggers: %w; original error: %w", allowedErr, err)
	}
	agentID := ""
	if agent != nil {
		agentID = agent.id
	}
	return fmt.Errorf("%w for agent %q: state=%s trigger=%s allowed=%v: %w", errIllegalStateTransition, agentID, before, trigger, allowed, err)
}

// allowedTriggersForState 返回当前状态允许的触发器，供错误消息说明可选路径。
func allowedTriggersForState(ctx context.Context, agent *agentRuntime, state string) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if agent != nil && agent.sm != nil {
		allowed, err := platformstatemachine.AllowedTriggers(agent.sm, ctx)
		if err != nil {
			return nil, err
		}
		return allowed, nil
	}
	if strings.TrimSpace(state) == "" {
		return nil, nil
	}
	return agentdto.AllowedTriggersStr(state), nil
}

// fireAndPublishLocked 触发状态机并发布 StateChanged 事件。
// 调用方持有 agentRegistry.mu；当前事件库异步扇出，订阅者即使回调 service 也不会在同一 goroutine 抢锁。
// 注意：当前 kelindar/event 发布会异步扇出，所以持锁发布不会回调抢锁。
// 如果事件库改为同步派发，这里必须迁到锁外或改成 trigger channel，避免死锁。
func (s *service) fireAndPublishLocked(ctx context.Context, agent *agentRuntime, trigger agentdto.AgentTrigger) error {
	before := string(agent.state)
	if err := agent.sm.FireCtx(ctx, stateless.Trigger(string(trigger))); err != nil {
		return err
	}
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt, agent.startedAt)
	s.publishStateChanged(agent, before, string(trigger))
	return nil
}

func agentSessionFenceOK(agent *agentState, evSessionID string) bool {
	if agent == nil {
		return false
	}
	ev := strings.TrimSpace(evSessionID)
	if ev == "" {
		return true
	}
	return ev == agentSessionID(agent)
}

func decodeLegacyAlias[C any, L any](
	raw []byte,
	current *C,
	aliasFn func(*C, *L) error,
) error {
	return decodeLegacyAliasWith(raw, current, aliasFn, json.Unmarshal)
}

func decodeLegacyAliasWith[C any, L any](
	raw []byte,
	current *C,
	aliasFn func(*C, *L) error,
	decode func([]byte, any) error,
) error {
	if decode == nil {
		decode = json.Unmarshal
	}
	if err := decode(raw, current); err != nil {
		return err
	}
	var legacy L
	if err := decode(raw, &legacy); err != nil {
		return err
	}
	return aliasFn(current, &legacy)
}

func agentSessionID(agent *agentState) string {
	if agent == nil || agent.launchSeq == 0 {
		return ""
	}
	return strconv.FormatUint(agent.launchSeq, 10)
}
