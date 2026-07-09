package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/qmuntal/stateless"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/exitmonitor"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/processctl"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformstatemachine "github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
)

const submitSessionReadyTimeout = 5 * time.Second

const longWaitLogThreshold = 2 * time.Second

func buildStatesFromDefinitions(defs []agentdto.TransitionDefinition) []platformstatemachine.StateConfig {
	permits := make(map[string][]platformstatemachine.Permit, len(agentdto.StateDefinitions))
	for _, def := range defs {
		permits[string(def.From)] = append(permits[string(def.From)], platformstatemachine.Permit{
			Trigger: string(def.Trigger),
			Dest:    string(def.To),
		})
	}
	states := make([]platformstatemachine.StateConfig, 0, len(agentdto.StateDefinitions))
	for _, def := range agentdto.StateDefinitions {
		states = append(states, platformstatemachine.StateConfig{
			Name:    string(def.Name),
			Permits: permits[string(def.Name)],
		})
	}
	return states
}

// BindActiveTurnID 把当前活跃 turn 绑定到 provider 返回的真实 turn ID。
func (s *service) BindActiveTurnID(ctx context.Context, agentID, turnID string) error {
	if s == nil || s.turns == nil {
		return errors.New("turn controller is not configured")
	}
	return s.turns.BindActiveTurnID(ctx, agentID, turnID)
}

// BindActiveTurnID 把当前 active turn 绑定到 provider 返回的真实 turn ID。
func (c *turnController) BindActiveTurnID(ctx context.Context, agentID, turnID string) error {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return errors.New("turn id is required")
	}
	return c.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if agent.activeTurnID == "" {
			return fmt.Errorf("%w: agent %q has no active turn", errTurnNotActive, agent.id)
		}
		if agent.activeTurnID == turnID {
			return nil
		}
		agent.activeTurnID = turnID
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
		return nil
	})
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
	c.finishTurnStartSuccess(ctx, work, startedTurnID)
}

// finishTurnStartSuccess 记录 provider 接受的 turn ID，并把状态推进到运行中。
func (c *turnController) finishTurnStartSuccess(ctx context.Context, work turnWork, startedTurnID string) {
	currentTurnID := strings.TrimSpace(startedTurnID)
	if currentTurnID == "" {
		currentTurnID = work.turnID
	}
	if currentTurnID != work.turnID {
		if err := c.BindActiveTurnID(ctx, work.agentID, currentTurnID); err != nil {
			c.log().Warn("orchestration: failed to bind started turn id", "agent_id", work.agentID, "turn_id", currentTurnID, "error", err)
			return
		}
	}
	if lockErr := c.withAgentLocked(work.agentID, func(agent *agentRuntime) error {
		if agent.activeTurnID != currentTurnID {
			return nil
		}
		if err := c.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted); err != nil {
			c.log().Warn("orchestration: failed to mark turn running", "agent_id", agent.id, "turn_id", currentTurnID, "error", err)
		}
		return nil
	}); lockErr != nil {
		c.log().Warn("orchestration: finish turn start success lock failed",
			"agent_id", work.agentID, "turn_id", currentTurnID, "error", lockErr)
	}
}

func (c *turnController) finishTurnStartFailure(ctx context.Context, work turnWork, startErr error) {
	if lockErr := c.withAgentLocked(work.agentID, func(agent *agentRuntime) error {
		if agent.activeTurnID != work.turnID {
			return nil
		}
		agent.lastError = startErr.Error()
		agent.activeTurnID = ""
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
