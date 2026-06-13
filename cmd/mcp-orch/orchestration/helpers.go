package orchestration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/qmuntal/stateless"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/exitmonitor"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/processctl"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	platformstatemachine "github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
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
