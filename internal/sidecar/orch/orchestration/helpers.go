package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/qmuntal/stateless"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	platformstatemachine "github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/orchestration/exitmonitor"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/orchestration/processctl"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/taskdag"
)

const submitSessionReadyTimeout = 5 * time.Second

const longWaitLogThreshold = 2 * time.Second

// P22 P4 S4a: the local `sessionReadyWaiter` interface was deleted.
// WaitForSessionReady is now part of the owner contract
// contract.OrchestrationTurnStarter, so the service calls it directly
// without a type-assertion side-channel. See
// internal/contract/orchestration.go for the interface definition and
// docs/plans/迁移/p22/P4_DependencyDirectionAndHiddenContracts.md
// §279 for the rationale.

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
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return errors.New("turn id is required")
	}
	return s.withAgentLocked(agentID, func(agent *agentRuntime) error {
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

// P22 P3: claimMonitorTargets is deleted. Exit monitoring is now armed at
// launch time by startProcessLocked (and the launcher bridge for remote
// launches) via exitmonitor.Monitor.Arm; the runnerActor is a pure consumer
// of monitor.ExitEvents() and no longer polls for unmonitored cmds.

// reconcileReadyStateLocked 在进程已就绪时修正本地状态和队列状态。
func (s *service) reconcileReadyStateLocked(ctx context.Context, agent *agentRuntime) {
	if agent.cmd == nil || agent.stopRequested {
		return
	}
	if agent.activeTurnID == "" && agent.state == agentdto.StateIdle && agent.queue.Len() > 0 {
		if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnEnqueued); err != nil {
			s.logger.Warn("orchestration: failed to mark queued turn", "agent_id", agent.id, "error", err)
		}
		return
	}
	if agent.activeTurnID != "" {
		return
	}
	switch agent.state {
	case agentdto.StateTurnStarting, agentdto.StateTurnRunning:
		if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnCompleted); err != nil {
			s.logger.Warn("orchestration: failed to reconcile ready state", "agent_id", agent.id, "state", agent.state, "error", err)
		}
	}
}

// startTurnExecution 等待 session 可提交后，把排队的 turn 交给 provider 执行。
func (s *service) startTurnExecution(ctx context.Context, work turnWork) {
	startedAt := time.Now()
	logger := pkglogger.FromContext(ctx)
	if s != nil && s.logger != nil {
		logger = s.logger
	}
	logger.Info("orchestration: turn execution start",
		pkglogger.String(pkglogger.FieldAgentID, work.agentID),
		pkglogger.String(pkglogger.FieldThreadID, work.threadID),
		pkglogger.String(pkglogger.FieldTurnID, work.turnID),
		pkglogger.String(pkglogger.FieldComponent, "submit_turn"))
	if err := s.waitForSubmitSessionReady(ctx, work.agentID); err != nil {
		s.finishTurnStartFailure(ctx, work, err)
		return
	}
	if s.turnStarter == nil {
		s.finishTurnStartFailure(ctx, work, errors.New("turn starter is not configured"))
		return
	}
	startedTurnID, err := s.turnStarter.StartTurn(ctx, work.submission)
	if err != nil {
		logger.Warn("orchestration: turn execution start failed",
			pkglogger.String(pkglogger.FieldAgentID, work.agentID),
			pkglogger.String(pkglogger.FieldThreadID, work.threadID),
			pkglogger.String(pkglogger.FieldTurnID, work.turnID),
			pkglogger.String(pkglogger.FieldError, err.Error()),
			pkglogger.Int64(pkglogger.FieldDurationMS, time.Since(startedAt).Milliseconds()))
		s.finishTurnStartFailure(ctx, work, err)
		return
	}
	logger.Info("orchestration: turn execution accepted",
		pkglogger.String(pkglogger.FieldAgentID, work.agentID),
		pkglogger.String(pkglogger.FieldThreadID, work.threadID),
		pkglogger.String(pkglogger.FieldTurnID, work.turnID),
		pkglogger.String(pkglogger.FieldComponent, "submit_turn"),
		pkglogger.String("started_turn_id", strings.TrimSpace(startedTurnID)),
		pkglogger.Int64(pkglogger.FieldDurationMS, time.Since(startedAt).Milliseconds()))
	s.finishTurnStartSuccess(ctx, work, startedTurnID)
}

// finishTurnStartSuccess 记录 provider 接受的 turn ID，并把状态推进到运行中。
func (s *service) finishTurnStartSuccess(ctx context.Context, work turnWork, startedTurnID string) {
	currentTurnID := strings.TrimSpace(startedTurnID)
	if currentTurnID == "" {
		currentTurnID = work.turnID
	}
	if currentTurnID != work.turnID {
		if err := s.BindActiveTurnID(ctx, work.agentID, currentTurnID); err != nil {
			s.logger.Warn("orchestration: failed to bind started turn id", "agent_id", work.agentID, "turn_id", currentTurnID, "error", err)
			return
		}
	}
	if lockErr := s.withAgentLocked(work.agentID, func(agent *agentRuntime) error {
		if agent.activeTurnID != currentTurnID {
			return nil
		}
		if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted); err != nil {
			s.logger.Warn("orchestration: failed to mark turn running", "agent_id", agent.id, "turn_id", currentTurnID, "error", err)
		}
		return nil
	}); lockErr != nil {
		s.logger.Warn("orchestration: finish turn start success lock failed",
			"agent_id", work.agentID, "turn_id", currentTurnID, "error", lockErr)
	}
}

func (s *service) finishTurnStartFailure(ctx context.Context, work turnWork, startErr error) {
	if lockErr := s.withAgentLocked(work.agentID, func(agent *agentRuntime) error {
		if agent.activeTurnID != work.turnID {
			return nil
		}
		agent.lastError = startErr.Error()
		agent.activeTurnID = ""
		if agent.state != agentdto.StateTurnStarting {
			return nil
		}
		if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnCompleted); err != nil {
			s.logger.Warn("orchestration: failed to reset turn after start error", "agent_id", agent.id, "turn_id", work.turnID, "error", err)
		}
		return nil
	}); lockErr != nil {
		s.logger.Warn("orchestration: finish turn start failure lock failed",
			"agent_id", work.agentID, "turn_id", work.turnID, "error", lockErr)
	}
}

func (s *service) stopAgentLocked(ctx context.Context, agent *agentRuntime, reason string) error {
	changed, err := s.markStoppingLocked(ctx, agent, reason)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return processctl.RequestStop(agent.cmd, agent.processGuard)
}

func (s *service) stopAgentWithReason(ctx context.Context, agentID, reason string) error {
	launchSeq, err := s.requestAgentStop(ctx, agentID, reason)
	if err != nil {
		return err
	}
	return s.waitForProcessExit(ctx, strings.TrimSpace(agentID), launchSeq)
}

func (s *service) requestAgentStop(ctx context.Context, agentID, reason string) (uint64, error) {
	launchSeq := uint64(0)
	err := s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if agent.cmd != nil {
			launchSeq = agent.launchSeq
		}
		return s.stopAgentLocked(ctx, agent, reason)
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

func (s *service) submitAgentReadyState(ctx context.Context, agentID string) (bool, error) {
	ready := false
	err := s.withAgentReadLocked(agentID, func(agent *agentRuntime) error {
		if !s.agentRunningLocked(ctx, agent) {
			return fmt.Errorf("agent %q is not running", agent.id)
		}
		if agent.stopRequested {
			return fmt.Errorf("agent %q is stopping", agent.id)
		}
		ready = agent.state == agentdto.StateIdle && agent.activeTurnID == "" && agent.queue.Len() == 0
		return nil
	})
	return ready, err
}

// waitForSubmitSessionReady 在提交 turn 前等待 provider session 完成启动。
func (s *service) waitForSubmitSessionReady(ctx context.Context, agentID string) error {
	if s == nil || s.turnStarter == nil {
		return nil
	}
	startedAt := time.Now()
	logger := pkglogger.FromContext(ctx)
	if s.logger != nil {
		logger = s.logger
	}
	logger.Info("orchestration: waiting for submit session ready",
		pkglogger.String(pkglogger.FieldAgentID, agentID),
		pkglogger.String(pkglogger.FieldComponent, "submit_turn"),
		pkglogger.Int64(pkglogger.FieldDurationMS, submitSessionReadyTimeout.Milliseconds()))
	err := s.turnStarter.WaitForSessionReady(ctx, agentID, submitSessionReadyTimeout)
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
	cmd := exec.Command(agent.command[0], agent.command[1:]...)
	cmd.Dir = agent.cwd
	cmd.Env = append(contract.ScrubDatabaseEnv(os.Environ()), contract.ScrubDatabaseEnv(agent.env)...)
	processctl.Configure(cmd)
	if err := cmd.Start(); err != nil {
		return s.commitLaunchFailureLocked(ctx, agent, err)
	}
	guard := processctl.Attach(cmd, s.logger)
	now := resolveEventTime(ctx, agent.updatedAt)
	resetLaunchState(agent)
	agent.cmd = cmd
	agent.processGuard = guard
	agent.launchSeq++
	agent.startedAt = now
	agent.updatedAt = now
	if err := s.commitLaunchSuccessLocked(ctx, agent); err != nil {
		if stopErr := processctl.ForceStop(cmd, guard); stopErr != nil {
			s.logger.Warn("orchestration: rollback stop process failed",
				"agent_id", agent.id, "error", stopErr)
		}
		if guard != nil {
			guard.Close()
		}
		agent.cmd = nil
		agent.processGuard = nil
		return err
	}
	// P22 P3: arm the exit monitor immediately after a successful Start. The
	// monitor spawns a tracked cmd.Wait goroutine that publishes exactly one
	// exit event per (agent.id, agent.launchSeq) onto ExitEvents(); the
	// runnerActor's main loop consumes that stream. monitoredSeq is kept as a
	// test-visible flag so existing polling-based assertions keep working.
	if s.exitMonitor != nil {
		s.exitMonitor.Arm(exitmonitor.Target{
			AgentID:   agent.id,
			LaunchSeq: agent.launchSeq,
			Cmd:       cmd,
		})
	}
	agent.monitoredSeq = agent.launchSeq
	s.logger.Info("orchestration: agent launched", "agent_id", agent.id, "pid", cmd.Process.Pid)
	return nil
}

// fireOrForceLocked 触发状态机，并把非法迁移包装成带上下文的错误。
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
	return fmt.Errorf("%w for agent %q: state=%s trigger=%s allowed=%s: %w", errIllegalStateTransition, agentID, before, trigger, strings.Join(allowed, ","), err)
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

// fireAndPublishLocked fires a state-machine trigger and publishes a
// StateChanged event while the caller holds s.mu. publishStateChanged calls
// Dispatcher.Publish, which (kelindar/event) fans out to subscriber goroutines
// asynchronously. Those subscribers may in turn call back into the service and
// attempt to acquire s.mu.
//
// TODO(convention): Publish 在持锁期间调用存在潜在风险——如果 kelindar/event
// 的投递策略变更为同步，或 subscriber 在同一 goroutine 回调，将导致死锁。
// 应将 Publish 移到锁外，或改为 trigger channel 解耦（参见 statemachine-event-convention B7）。
func (s *service) fireAndPublishLocked(ctx context.Context, agent *agentRuntime, trigger agentdto.AgentTrigger) error {
	before := string(agent.state)
	if err := agent.sm.FireCtx(ctx, stateless.Trigger(string(trigger))); err != nil {
		return err
	}
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt, agent.startedAt)
	s.publishStateChanged(agent, before, string(trigger))
	return nil
}

func (s *service) listAgents() []agentRuntime {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agents := make([]agentRuntime, 0, len(s.agents))
	for _, agent := range s.agents {
		snapshot := *agent
		snapshot.queue = nil
		snapshot.sm = nil
		snapshot.exitedAt = shared.CloneTime(agent.exitedAt)
		agents = append(agents, snapshot)
	}
	return agents
}

// Agent lookup and shared decode helpers moved from factory.go to keep orchestration factory focused.
func (s *service) discardStaleSuccessfulLaunch(ctx context.Context, launching *agentRuntime, staleErr error) error {
	if stopErr := s.launcher.Stop(ctx, launching); stopErr != nil {
		s.logger.Warn("orchestration: discard stale successful launch stop failed", "agent_id", launching.id, "error", stopErr)
	}
	return staleErr
}

func (s *service) withAgentLocked(agentID string, fn func(*agentState) error) error {
	if s == nil {
		return fmt.Errorf("%w: %s", errAgentNotFound, strings.TrimSpace(agentID))
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := lookupAgentByIDLocked(s.agents, agentID)
	if err != nil {
		return err
	}
	return fn(agent)
}

func (s *service) withAgentReadLocked(agentID string, fn func(*agentState) error) error {
	if s == nil {
		return fmt.Errorf("%w: %s", errAgentNotFound, strings.TrimSpace(agentID))
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	agent, err := lookupAgentByIDLocked(s.agents, agentID)
	if err != nil {
		return err
	}
	return fn(agent)
}

func (s *service) withAgentReadLockedByAgentID(ctx context.Context, agentID string, fn func(*agentState) error) error {
	if s == nil {
		return fmt.Errorf("%w: %s", errAgentNotFound, strings.TrimSpace(agentID))
	}
	if err := s.lockRead(ctx); err != nil {
		return err
	}
	defer s.mu.RUnlock()

	agent, err := lookupAgentByIdentityLocked(s.agents, agentID, agentIdentityLocalOnly)
	if err != nil {
		return err
	}
	return fn(agent)
}

func (s *service) lockRead(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return s.mu.RLockCtx(ctx)
}

func (s *service) runtimeAgentSnapshots(ctx context.Context) ([]AgentSnapshot, error) {
	if err := s.lockRead(ctx); err != nil {
		return nil, err
	}
	defer s.mu.RUnlock()
	snapshots := make([]AgentSnapshot, 0, len(s.agents))
	for _, agent := range s.agents {
		snapshots = append(snapshots, s.snapshotLocked(ctx, agent))
	}
	return snapshots, nil
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

func lookupAgentBySeqLocked(
	agents map[string]*agentState,
	agentID string,
	launchSeq uint64,
) (*agentState, error) {
	agent, err := lookupAgentByIDLocked(agents, agentID)
	if err != nil {
		return nil, err
	}
	if agent.launchSeq != launchSeq {
		return nil, fmt.Errorf("%w: %s/%d", errAgentNotFound, strings.TrimSpace(agentID), launchSeq)
	}
	return agent, nil
}

func (s *service) withDAGStore(fn func(taskdag.OrchestrationStore) error) error {
	if s == nil || s.dagStore == nil {
		return errors.New("dag store is not configured")
	}
	return fn(s.dagStore)
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
