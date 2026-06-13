package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/processctl"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	recoverReasonManual      = "manual"
	recoverReasonStall       = "stall_detected"
	recoverReasonProcessExit = "process_exit_error"
	turnResumeReasonRecover  = "recover_succeeded"
)

type StallDetector struct {
	threshold time.Duration
	logger    *slog.Logger
}

// CheckStall 处理checkstall。
func (d *StallDetector) CheckStall(agent *agentRuntime) bool {
	if agent.state != agentdto.StateTurnRunning {
		return false
	}
	stalled := time.Since(agent.updatedAt) > d.threshold
	if stalled && d.logger != nil {
		d.logger.Warn("orchestration: stalled agent detected", "agent_id", agent.id)
	}
	return stalled
}

// Recover restarts the agent process and replays persisted DAG-backed work when
// the stored wakeup is still fenced to the same active turn.
// Recover 恢复编排。
func (s *service) Recover(ctx context.Context, agentID string) error {
	return s.recoverWithReason(ctx, agentID, recoverReasonManual)
}

func (s *service) recoverWithReason(ctx context.Context, agentID, reason string) error {
	if s.canRecoverAgentViaLauncher(ctx, agentID) {
		return s.recoverLauncherWithReason(ctx, agentID, reason)
	}
	return s.recoverLocalWithReason(ctx, agentID, reason)
}

func (s *service) recoverLocalWithReason(ctx context.Context, agentID, reason string) error {
	return s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		threadID := agent.threadID
		turnID := agent.activeTurnID
		emitEvent(s.eventBus, eventTypeAgentRecovering, eventAgentID(agent), agent, reason)
		resumed, err := recoverAgent(ctx, s, agent)
		if err != nil {
			return err
		}
		if resumed {
			resumedAt := time.Now()
			s.suppressStoppedHookThreadUntilLocked(threadID, resumedAt)
			s.publishTurnResumed(agent, threadID, turnID, turnResumeReasonRecover, resolveEventTime(ctx, resumedAt))
		}
		s.logger.Info("orchestration: agent recovered", "agent_id", agent.id, "pid", processPID(agent.cmd))
		return nil
	})
}

// recoverAgent 恢复代理。
func recoverAgent(ctx context.Context, s *service, agent *agentRuntime) (bool, error) {
	activeTurnID := strings.TrimSpace(agent.activeTurnID)
	replay, shouldReplay, err := loadRecoveredTurnSubmission(ctx, s, agent)
	if err != nil {
		return false, err
	}
	if err := processctl.ForceStop(agent.cmd, agent.processGuard); err != nil {
		return false, err
	}
	closeAgentProcessGuard(agent)
	agent.stopRequested = false
	agent.activeTurnID = ""
	agent.monitoredSeq = 0
	if err := normalizeRecoveryState(ctx, s, agent); err != nil {
		return false, err
	}
	if err := s.startProcessLocked(ctx, agent); err != nil {
		return false, err
	}
	if !shouldReplay {
		if shouldWriteRecoveryNoReplayFallback(agent, activeTurnID) {
			return false, s.setNoReportFallbackLocked(ctx, agent)
		}
		return false, nil
	}
	if err := replayRecoveredTurn(ctx, s, agent, replay); err != nil {
		return false, err
	}
	s.logger.Info(
		"orchestration: queued recovered active turn replay",
		"agent_id", agent.id,
		"turn_id", replay.ExpectedTurnID,
	)
	return true, nil
}

func shouldWriteRecoveryNoReplayFallback(agent *agentRuntime, activeTurnID string) bool {
	return agent != nil && strings.TrimSpace(agent.lastReport) == "" &&
		(strings.TrimSpace(activeTurnID) != "" || len(agent.reportRequesters) > 0)
}

func normalizeRecoveryState(ctx context.Context, s *service, agent *agentRuntime) error {
	return s.fireOrForceLocked(ctx, agent, agentdto.TriggerRecoverRequested)
}

// Replay is currently supported only when the active turn can be reconstructed
// from persisted DAG wakeup payloads.
func loadRecoveredTurnSubmission(ctx context.Context, s *service, agent *agentRuntime) (TurnSubmission, bool, error) {
	activeTurnID, ok := validateRecoveryContext(s, agent)
	if !ok {
		return TurnSubmission{}, false, nil
	}
	wakeup, err := findReplayWakeup(ctx, s, agent, activeTurnID)
	if err != nil {
		return TurnSubmission{}, false, err
	}
	if wakeup == nil {
		return TurnSubmission{}, false, nil
	}
	submission, err := decodeReplayWakeupSubmission(wakeup, agent, activeTurnID)
	if err != nil {
		return TurnSubmission{}, false, err
	}
	return submission, true, nil
}

// findReplayWakeup 查找replaywakeup。
func findReplayWakeup(ctx context.Context, s *service, agent *agentRuntime, activeTurnID string) (*taskdag.Wakeup, error) {
	nodes, err := s.recoveryStore.ListRunningNodesByAssignee(ctx, agent.id)
	if err != nil {
		return nil, fmt.Errorf("recover replay: list running nodes for %q: %w", agent.id, err)
	}
	for _, node := range nodes {
		if !nodeMatchesActiveTurn(node, activeTurnID) {
			continue
		}
		wakeup, err := loadReplayWakeup(ctx, s, node, activeTurnID)
		if err != nil {
			return nil, err
		}
		if wakeupEligibleForReplay(agent, activeTurnID, wakeup) {
			return wakeup, nil
		}
	}
	return nil, nil
}

func nodeMatchesActiveTurn(node taskdag.Node, activeTurnID string) bool {
	return node.ActiveTurnID != nil && strings.TrimSpace(*node.ActiveTurnID) == activeTurnID
}

func loadReplayWakeup(ctx context.Context, s *service, node taskdag.Node, activeTurnID string) (*taskdag.Wakeup, error) {
	if node.ActiveWakeupID == nil || *node.ActiveWakeupID <= 0 {
		return nil, fmt.Errorf("recover replay: node %s/%s missing active wakeup for turn %q", node.DagKey, node.NodeKey, activeTurnID)
	}
	wakeup, err := s.recoveryStore.GetWakeup(ctx, *node.ActiveWakeupID)
	if err != nil {
		return nil, fmt.Errorf("recover replay: load wakeup %d for turn %q: %w", *node.ActiveWakeupID, activeTurnID, err)
	}
	return wakeup, nil
}

func decodeReplayWakeupSubmission(wakeup *taskdag.Wakeup, agent *agentRuntime, activeTurnID string) (TurnSubmission, error) {
	submission, err := decodeRecoveredTurnSubmission(wakeup.PromptPayload, agent, activeTurnID)
	if err != nil {
		return TurnSubmission{}, fmt.Errorf("recover replay: decode wakeup %d for turn %q: %w", wakeup.ID, activeTurnID, err)
	}
	return submission, nil
}

func validateRecoveryContext(s *service, agent *agentRuntime) (string, bool) {
	if s == nil || agent == nil {
		return "", false
	}
	activeTurnID := strings.TrimSpace(agent.activeTurnID)
	if activeTurnID == "" || s.recoveryStore == nil {
		return "", false
	}
	return activeTurnID, true
}

// wakeupEligibleForReplay 为replay处理wakeupeligible。
func wakeupEligibleForReplay(agent *agentRuntime, activeTurnID string, wakeup *taskdag.Wakeup) bool {
	if wakeup == nil || strings.TrimSpace(wakeup.Status) != "sent" {
		return false
	}
	if wakeup.BoundTurnID == nil || strings.TrimSpace(*wakeup.BoundTurnID) != activeTurnID {
		return false
	}
	if wakeup.TurnBoundAt == nil {
		return false
	}
	targetAgentID := strings.TrimSpace(wakeup.TargetAgentID)
	return targetAgentID == "" || agent == nil || targetAgentID == agent.id
}

func decodeRecoveredTurnSubmission(raw json.RawMessage, agent *agentRuntime, activeTurnID string) (TurnSubmission, error) {
	raw = append(json.RawMessage(nil), raw...)
	var direct TurnSubmission
	if err := json.Unmarshal(raw, &direct); err == nil && len(direct.Inputs) > 0 {
		return normalizeRecoveredTurnSubmission(agent, activeTurnID, direct), nil
	}
	var params submitParams
	if err := json.Unmarshal(raw, &params); err == nil {
		items, decodeErr := inputItemsFromSubmitParams(params)
		if decodeErr != nil {
			return TurnSubmission{}, decodeErr
		}
		return normalizeRecoveredTurnSubmission(agent, activeTurnID, TurnSubmission{
			AgentID:              strings.TrimSpace(params.AgentID),
			Inputs:               items,
			SelectedSkills:       append([]string(nil), params.SelectedSkills...),
			ManualSkillSelection: params.ManualSkillSelection,
			OutputSchema:         append(json.RawMessage(nil), params.OutputSchema...),
		}), nil
	}
	return TurnSubmission{}, fmt.Errorf("unsupported prompt payload shape")
}

func normalizeRecoveredTurnSubmission(agent *agentRuntime, activeTurnID string, submission TurnSubmission) TurnSubmission {
	normalized := cloneTurnSubmission(submission)
	if agent != nil {
		normalized.AgentID = shared.FirstTrimmed(normalized.AgentID, agent.id)
		normalized.ThreadID = shared.FirstTrimmed(normalized.ThreadID, agent.threadID, agent.id)
	} else {
		normalized.AgentID = shared.FirstTrimmed(normalized.AgentID)
		normalized.ThreadID = shared.FirstTrimmed(normalized.ThreadID)
	}
	normalized.ExpectedTurnID = shared.FirstTrimmed(normalized.ExpectedTurnID, activeTurnID)
	return normalized
}

func replayRecoveredTurn(ctx context.Context, s *service, agent *agentRuntime, submission TurnSubmission) error {
	if agent == nil {
		return nil
	}
	if agent.queue == nil {
		agent.queue = &SubmissionQueue{}
	}
	agent.queue.Prepend(submission)
	return s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnEnqueued)
}

// Launcher-backed recovery helpers moved from factory.go to keep orchestration factory focused.
type launcherRecoveryAttempt struct {
	agentID, threadID, turnID string
	expectedSeq               uint64
	launching                 agentRuntime
	replay                    TurnSubmission
	shouldReplay              bool
	req                       LaunchRequest
}

func (s *service) canRecoverAgentViaLauncher(ctx context.Context, agentID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	agent, err := lookupAgentByIDLocked(s.agents, agentID)
	return err == nil && shouldRecoverViaLauncher(ctx, s, agent)
}

func (s *service) recoverLauncherWithReason(ctx context.Context, agentID, reason string) error {
	attempt, err := s.prepareLauncherRecovery(ctx, agentID, reason)
	if err != nil {
		return err
	}
	replay, shouldReplay, err := loadRecoveredTurnSubmission(ctx, s, &attempt.launching)
	if err != nil {
		return s.commitLauncherRecoveryFailure(ctx, attempt, err)
	}
	attempt.replay, attempt.shouldReplay = replay, shouldReplay
	if err := s.launcher.Stop(ctx, &attempt.launching); err != nil {
		return s.commitLauncherRecoveryFailure(ctx, attempt, err)
	}
	result, err := s.launcher.Launch(ctx, &attempt.launching, attempt.req)
	if err != nil {
		return s.commitLauncherRecoveryFailure(ctx, attempt, err)
	}
	return s.commitLauncherRecoverySuccess(ctx, attempt, result)
}

func (s *service) prepareLauncherRecovery(ctx context.Context, agentID, reason string) (launcherRecoveryAttempt, error) {
	var attempt launcherRecoveryAttempt
	err := s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if !shouldRecoverViaLauncher(ctx, s, agent) {
			return fmt.Errorf("agent %q is not running under launcher", agent.id)
		}
		threadID, turnID := agent.threadID, agent.activeTurnID
		if err := normalizeRecoveryState(ctx, s, agent); err != nil {
			return err
		}
		agent.launchSeq++
		agent.pendingLaunchThreadID, agent.pendingLaunchThreadAt = "", time.Time{}
		emitEvent(s.eventBus, eventTypeAgentRecovering, eventAgentID(agent), agent, reason)
		attempt = launcherRecoveryAttempt{
			agentID: agent.id, expectedSeq: agent.launchSeq, launching: *agent,
			threadID: threadID, turnID: turnID, req: recoveryLaunchRequest(agent),
		}
		return nil
	})
	return attempt, err
}

func (s *service) commitLauncherRecoveryFailure(ctx context.Context, attempt launcherRecoveryAttempt, launchErr error) error {
	s.mu.Lock()
	agent, err := lookupAgentBySeqLocked(s.agents, attempt.agentID, attempt.expectedSeq)
	if err != nil {
		s.mu.Unlock()
		return s.discardStaleLaunchResult(ctx, &attempt.launching, launchErr)
	}
	err = s.commitLaunchFailureLocked(ctx, agent, launchErr)
	if fallbackErr := s.setNoReportFallbackLocked(ctx, agent); fallbackErr != nil {
		err = errors.Join(err, fallbackErr)
	}
	s.mu.Unlock()
	return err
}

// commitLauncherRecoverySuccess 处理commit启动器recoverysuccess。
func (s *service) commitLauncherRecoverySuccess(ctx context.Context, attempt launcherRecoveryAttempt, result LaunchResult) error {
	s.mu.Lock()
	agent, err := lookupAgentBySeqLocked(s.agents, attempt.agentID, attempt.expectedSeq)
	if err != nil || agent.state != agentdto.StateRecovering || agent.stopRequested {
		s.mu.Unlock()
		return s.discardStaleSuccessfulLaunch(ctx, &attempt.launching, err)
	}
	adoptLaunchStateLocked(agent, &attempt.launching)
	bindLaunchResult(agent, result)
	agent.activeTurnID, agent.monitoredSeq = "", 0
	agent.stopRequested = false
	if err := s.rekeyLaunchedAgentLocked(agent); err != nil {
		commitErr := s.commitLaunchFailureLocked(ctx, agent, err)
		s.mu.Unlock()
		if stopErr := s.launcher.Stop(ctx, &attempt.launching); stopErr != nil {
			s.logger.Warn("orchestration: recovery rekey failure cleanup stop failed", "agent_id", attempt.launching.id, "error", stopErr)
		}
		return commitErr
	}
	if err := s.commitLaunchSuccessLocked(ctx, agent); err != nil {
		closeAgentProcessGuard(agent)
		agent.cmd = nil
		agent.threadID = ""
		resetRuntimeStateLocked(agent)
		s.mu.Unlock()
		if stopErr := s.launcher.Stop(ctx, &attempt.launching); stopErr != nil {
			s.logger.Warn("orchestration: recovery success cleanup stop failed", "agent_id", attempt.launching.id, "error", stopErr)
		}
		return err
	}
	if err := s.finishLauncherRecoveryTurnLocked(ctx, agent, attempt); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	return nil
}

func (s *service) finishLauncherRecoveryTurnLocked(ctx context.Context, agent *agentRuntime, attempt launcherRecoveryAttempt) error {
	if !attempt.shouldReplay {
		if shouldWriteRecoveryNoReplayFallback(agent, attempt.turnID) {
			return s.setNoReportFallbackLocked(ctx, agent)
		}
		return nil
	}
	attempt.replay.AgentID, attempt.replay.ThreadID = agent.id, agent.threadID
	if err := replayRecoveredTurn(ctx, s, agent, attempt.replay); err != nil {
		return err
	}
	s.suppressStoppedHookThreadLocked(attempt.threadID)
	s.publishTurnResumed(agent, attempt.threadID, attempt.turnID, turnResumeReasonRecover, resolveEventTime(ctx, time.Now()))
	return nil
}

func (s *service) notifyRecoveryFailure(ctx context.Context, agentID string, recoverErr error) error {
	return s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if strings.TrimSpace(agent.lastReport) == "" {
			agent.lastError = strings.TrimSpace(recoverErr.Error())
			return s.setNoReportFallbackLocked(ctx, agent)
		}
		return nil
	})
}
