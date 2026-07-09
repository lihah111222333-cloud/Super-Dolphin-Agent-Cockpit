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

// recover reason 常量会写入事件日志，调用方靠它区分人工恢复、卡住检测和进程退出恢复。
const (
	recoverReasonManual      = "manual"
	recoverReasonStall       = "stall_detected"
	recoverReasonProcessExit = "process_exit_error"
	turnResumeReasonRecover  = "recover_succeeded"
)

// StallDetector 根据 agent 更新时间判断 turn_running 是否卡住。
// 它只报告 stalled，不直接恢复进程，恢复动作由调用方决定。
type StallDetector struct {
	threshold time.Duration // 超过该时长未更新即视为卡住
	logger    *slog.Logger  // 可选告警日志
}

// CheckStall 检查 agent 是否仍处于 turn_running 且超过卡住阈值。
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

// Recover 恢复 agent 运行态，并在 DAG wakeup 仍绑定同一 active turn 时重放待执行工作。
func (s *service) Recover(ctx context.Context, agentID string) error {
	return s.recoverWithReason(ctx, agentID, recoverReasonManual)
}

// recoverWithReason 按当前 agent 所属运行模式选择本地进程恢复或 launcher 恢复。
func (s *service) recoverWithReason(ctx context.Context, agentID, reason string) error {
	if s.canRecoverAgentViaLauncher(ctx, agentID) {
		return s.recoverLauncherWithReason(ctx, agentID, reason)
	}
	return s.recoverLocalWithReason(ctx, agentID, reason)
}

// recoverLocalWithReason 在 service 锁内停止旧进程、重建本地进程，并发布恢复事件。
// 若恢复前存在可重放的 DAG wakeup，成功后会发布 turn resumed 事件。
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

// recoverAgent 执行本地进程恢复的核心步骤：保存可重放 turn、强停旧进程、清理状态并重启。
// 调用方必须已持有 agent 锁，失败时保持错误向上传递，不吞掉不完整恢复。
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
			return false, s.reports.setNoReportFallbackLocked(ctx, agent)
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

// shouldWriteRecoveryNoReplayFallback 判断恢复后没有可重放 turn 时是否需要补写 no-report fallback。
func shouldWriteRecoveryNoReplayFallback(agent *agentRuntime, activeTurnID string) bool {
	return agent != nil && strings.TrimSpace(agent.lastReport) == "" &&
		(strings.TrimSpace(activeTurnID) != "" || len(agent.reportRequesters) > 0)
}

// normalizeRecoveryState 将 agent 状态推进到 recovering，保证后续 startProcess 使用一致状态。
func normalizeRecoveryState(ctx context.Context, s *service, agent *agentRuntime) error {
	return s.fireOrForceLocked(ctx, agent, agentdto.TriggerRecoverRequested)
}

// loadRecoveredTurnSubmission 从持久化 DAG wakeup 中恢复 active turn 的提交内容。
// 只有 wakeup 仍处于 sent 且绑定同一 turn 时才返回 shouldReplay=true。
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

// findReplayWakeup 查找当前 agent 下仍绑定 active turn 的可重放 wakeup。
func findReplayWakeup(ctx context.Context, s *service, agent *agentRuntime, activeTurnID string) (*taskdag.Wakeup, error) {
	nodes, err := s.lifecycle.recoveryStore.ListRunningNodesByAssignee(ctx, agent.id)
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

// nodeMatchesActiveTurn 判断运行中节点是否仍指向恢复前的 active turn。
func nodeMatchesActiveTurn(node taskdag.Node, activeTurnID string) bool {
	return node.ActiveTurnID != nil && strings.TrimSpace(*node.ActiveTurnID) == activeTurnID
}

// loadReplayWakeup 读取节点记录中的 active wakeup；缺失 wakeup 是恢复数据损坏，必须报错。
func loadReplayWakeup(ctx context.Context, s *service, node taskdag.Node, activeTurnID string) (*taskdag.Wakeup, error) {
	if node.ActiveWakeupID == nil || *node.ActiveWakeupID <= 0 {
		return nil, fmt.Errorf("recover replay: node %s/%s missing active wakeup for turn %q", node.DagKey, node.NodeKey, activeTurnID)
	}
	wakeup, err := s.lifecycle.recoveryStore.GetWakeup(ctx, *node.ActiveWakeupID)
	if err != nil {
		return nil, fmt.Errorf("recover replay: load wakeup %d for turn %q: %w", *node.ActiveWakeupID, activeTurnID, err)
	}
	return wakeup, nil
}

// decodeReplayWakeupSubmission 将 wakeup payload 解码为待重放的 turn submission。
func decodeReplayWakeupSubmission(wakeup *taskdag.Wakeup, agent *agentRuntime, activeTurnID string) (TurnSubmission, error) {
	submission, err := decodeRecoveredTurnSubmission(wakeup.PromptPayload, agent, activeTurnID)
	if err != nil {
		return TurnSubmission{}, fmt.Errorf("recover replay: decode wakeup %d for turn %q: %w", wakeup.ID, activeTurnID, err)
	}
	return submission, nil
}

// validateRecoveryContext 校验恢复重放所需的 service、agent、active turn 和 recovery store。
func validateRecoveryContext(s *service, agent *agentRuntime) (string, bool) {
	if s == nil || agent == nil {
		return "", false
	}
	activeTurnID := strings.TrimSpace(agent.activeTurnID)
	if activeTurnID == "" || s.lifecycle.recoveryStore == nil {
		return "", false
	}
	return activeTurnID, true
}

// wakeupEligibleForReplay 确认 wakeup 仍为 sent 且 fence 到同一 turn，避免重放过期任务。
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

// decodeRecoveredTurnSubmission 兼容解码新版 TurnSubmission 和旧版 submitParams payload。
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

// normalizeRecoveredTurnSubmission 补齐恢复重放所需的 agent、thread 和 expected turn 字段。
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

// replayRecoveredTurn 把恢复出的 submission 插回队列头部，并重新触发 turn_enqueued。
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

// launcherRecoveryAttempt 保存 launcher 恢复跨锁阶段所需的快照和 seq fence。
type launcherRecoveryAttempt struct {
	agentID, threadID, turnID string         // 恢复前的身份与 turn，用于提交后发布 resumed 事件
	expectedSeq               uint64         // launchSeq fence，防止旧启动结果覆盖新 agent
	launching                 agentRuntime   // 传给 launcher 的锁外运行快照
	replay                    TurnSubmission // 可选的恢复重放提交
	shouldReplay              bool           // 是否需要恢复 active turn
	req                       LaunchRequest  // 由恢复状态派生的 launcher 请求
}

// canRecoverAgentViaLauncher 判断当前 agent 是否由 launcher 管理且仍可远端恢复。
func (s *service) canRecoverAgentViaLauncher(ctx context.Context, agentID string) bool {
	registry := s.agentRegistry()
	registry.rLock()
	defer registry.rUnlock()
	agent, err := registry.lookupAgentByIDLocked(agentID)
	return err == nil && shouldRecoverViaLauncher(ctx, s, agent)
}

// recoverLauncherWithReason 停止远端 runtime 后重新 Launch，并按 seq fence 提交结果。
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
	if err := s.lifecycle.launcher.Stop(ctx, &attempt.launching); err != nil {
		return s.commitLauncherRecoveryFailure(ctx, attempt, err)
	}
	result, err := s.lifecycle.launcher.Launch(ctx, &attempt.launching, attempt.req)
	if err != nil {
		return s.commitLauncherRecoveryFailure(ctx, attempt, err)
	}
	return s.commitLauncherRecoverySuccess(ctx, attempt, result)
}

// prepareLauncherRecovery 在锁内进入 recovering 状态并复制 launcher 恢复所需快照。
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
		// recover 是新的显式启动周期，必须清掉上一轮 stop/archive 留下的停止意图。
		agent.stopRequested = false
		clearAgentStopReasonLocked(agent)
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

// commitLauncherRecoveryFailure 在恢复启动失败时写入失败状态和 no-report fallback。
func (s *service) commitLauncherRecoveryFailure(ctx context.Context, attempt launcherRecoveryAttempt, launchErr error) error {
	registry := s.agentRegistry()
	registry.lock()
	agent, err := registry.lookupAgentBySeqLocked(attempt.agentID, attempt.expectedSeq)
	if err != nil {
		registry.unlock()
		return s.discardStaleLaunchResult(ctx, &attempt.launching, launchErr)
	}
	err = s.commitLaunchFailureLocked(ctx, agent, launchErr)
	if fallbackErr := s.reports.setNoReportFallbackLocked(ctx, agent); fallbackErr != nil {
		err = errors.Join(err, fallbackErr)
	}
	registry.unlock()
	return err
}

// commitLauncherRecoverySuccess 在 seq fence 命中时采用新 launcher 状态并完成恢复。
// rekey 或持久化失败会主动停止新 runtime，避免留下孤儿远端 agent。
func (s *service) commitLauncherRecoverySuccess(ctx context.Context, attempt launcherRecoveryAttempt, result LaunchResult) error {
	registry := s.agentRegistry()
	registry.lock()
	agent, err := registry.lookupAgentBySeqLocked(attempt.agentID, attempt.expectedSeq)
	if err != nil || agent.state != agentdto.StateRecovering || agent.stopRequested {
		registry.unlock()
		return s.discardStaleSuccessfulLaunch(ctx, &attempt.launching, err)
	}
	adoptLaunchStateLocked(agent, &attempt.launching)
	bindLaunchResult(agent, result)
	agent.activeTurnID, agent.monitoredSeq = "", 0
	agent.stopRequested = false
	if err := s.rekeyLaunchedAgentLocked(agent); err != nil {
		commitErr := s.commitLaunchFailureLocked(ctx, agent, err)
		registry.unlock()
		if stopErr := s.lifecycle.launcher.Stop(ctx, &attempt.launching); stopErr != nil {
			s.logger.Warn("orchestration: recovery rekey failure cleanup stop failed", "agent_id", attempt.launching.id, "error", stopErr)
		}
		return commitErr
	}
	if err := s.commitLaunchSuccessLocked(ctx, agent); err != nil {
		closeAgentProcessGuard(agent)
		agent.cmd = nil
		agent.threadID = ""
		resetRuntimeStateLocked(agent)
		registry.unlock()
		if stopErr := s.lifecycle.launcher.Stop(ctx, &attempt.launching); stopErr != nil {
			s.logger.Warn("orchestration: recovery success cleanup stop failed", "agent_id", attempt.launching.id, "error", stopErr)
		}
		return err
	}
	if err := s.finishLauncherRecoveryTurnLocked(ctx, agent, attempt); err != nil {
		registry.unlock()
		return err
	}
	registry.unlock()
	return nil
}

// finishLauncherRecoveryTurnLocked 在恢复成功后补写 fallback 或重放 turn，并发布 resumed。
func (s *service) finishLauncherRecoveryTurnLocked(ctx context.Context, agent *agentRuntime, attempt launcherRecoveryAttempt) error {
	if !attempt.shouldReplay {
		if shouldWriteRecoveryNoReplayFallback(agent, attempt.turnID) {
			return s.reports.setNoReportFallbackLocked(ctx, agent)
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

// notifyRecoveryFailure 在自动恢复失败且没有 report 时写入可见 fallback，避免 UI 长期空白。
func (s *service) notifyRecoveryFailure(ctx context.Context, agentID string, recoverErr error) error {
	return s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if strings.TrimSpace(agent.lastReport) == "" {
			agent.lastError = strings.TrimSpace(recoverErr.Error())
			return s.reports.setNoReportFallbackLocked(ctx, agent)
		}
		return nil
	})
}
