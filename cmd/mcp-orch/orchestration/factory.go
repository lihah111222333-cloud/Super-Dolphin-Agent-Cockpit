package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

func resetLaunchState(agent *agentState) {
	if agent == nil {
		return
	}
	closeAgentProcessGuard(agent)
	agent.cmd = nil
	agent.monitoredSeq = 0
	agent.stopRequested = false
	clearAgentTurnStateLocked(agent)
	agent.remoteThreadID, agent.pendingLaunchThreadID = "", ""
	agent.pendingLaunchThreadAt, agent.remoteAgentID = time.Time{}, ""
	agent.startedAt = time.Time{}
	agent.updatedAt = time.Time{}
}

func cleanupAgentState(agent *agentState) {
	if agent == nil {
		return
	}
	if agent.queue != nil {
		agent.queue.Clear()
	}
	// Stop path: the active turn is being torn down, so turn-state fields
	// must all go to zero together. clearAgentTurnStateLocked covers
	// activeTurnID + threadID + exitedAt in one place.
	clearAgentTurnStateLocked(agent)
}

func (s *service) prepareLaunchLocked(ctx context.Context, agent *agentState) error {
	if agent == nil {
		return errAgentNotFound
	}
	if agent.queue != nil {
		agent.queue.Clear()
	}
	return s.prepareLaunchStateLocked(ctx, agent)
}

func (s *service) markStoppingLocked(ctx context.Context, agent *agentState, reason string) (bool, error) {
	if agent == nil {
		return false, errAgentNotFound
	}
	if agent.stopRequested {
		setStopReasonIfEmpty(agent, reason)
		return false, nil
	}
	if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerStopRequested); err != nil {
		return false, err
	}
	agent.stopRequested = true
	setStopReasonIfEmpty(agent, reason)
	cleanupAgentState(agent)
	return true, nil
}

func (s *service) commitLaunchFailureLocked(
	ctx context.Context,
	agent *agentState,
	launchErr error,
	details ...string,
) error {
	if launchErr == nil {
		return nil
	}
	if agent != nil {
		values := append(append([]string(nil), details...), launchErr.Error())
		agent.lastError = shared.FirstTrimmed(values...)
		s.logger.Warn("orchestration: launch failure committed",
			"agent_id", agent.id, "state", agent.state, "error", launchErr,
			"details", strings.Join(details, "; "))
	}
	if agent == nil {
		return launchErr
	}
	if fireErr := s.fireOrForceLocked(ctx, agent, agentdto.TriggerLaunchFailed); fireErr != nil {
		return errors.Join(launchErr, fireErr)
	}
	return launchErr
}

func (s *service) commitLaunchSuccessLocked(ctx context.Context, agent *agentState) error {
	if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerLaunchSucceeded); err != nil {
		if agent != nil {
			agent.lastError = err.Error()
		}
		return err
	}
	emitEvent(s.eventBus, eventTypeAgentLaunched, eventAgentID(agent), agent, agent.cwd)
	return nil
}

// finalizeActiveTurnLocked 结束当前活跃 turn，并按结果清理错误状态。
func (s *service) finalizeActiveTurnLocked(
	ctx context.Context,
	agent *agentState,
	turnID string,
	kind activeTurnFinalizationKind,
) error {
	if agent == nil {
		return errAgentNotFound
	}
	turnID = strings.TrimSpace(turnID)
	activeTurnID := strings.TrimSpace(agent.activeTurnID)
	if activeTurnID == "" {
		return errTurnNotActive
	}
	if turnID != "" && activeTurnID != turnID {
		return errTurnNotActive
	}
	if kind.clearError {
		agent.lastError = ""
	} else {
		agent.lastError = strings.TrimSpace(kind.errorText)
	}
	if err := s.fireOrForceLocked(ctx, agent, kind.trigger); err != nil {
		return err
	}
	agent.activeTurnID = ""
	return nil
}

// forceIdleAfterTurnTerminalLocked 在终态事件到达后强制回到可继续处理的空闲状态。
func (s *service) forceIdleAfterTurnTerminalLocked(
	ctx context.Context,
	agent *agentState,
	turnID string,
	kind activeTurnRecoveryKind,
) (bool, error) {
	if agent == nil {
		return false, errAgentNotFound
	}
	if !canForceIdleAfterTurnTerminal(agent, turnID) {
		return false, errTurnNotActive
	}
	before := agent.state
	agent.activeTurnID = ""
	if kind.clearError {
		agent.lastError = ""
	} else {
		agent.lastError = strings.TrimSpace(kind.errorText)
	}
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
	if kind.recover != nil {
		if err := kind.recover(ctx, s, agent); err != nil {
			return false, err
		}
	}
	if before != agent.state && strings.TrimSpace(kind.recoveredTrigger) != "" {
		s.publishStateChanged(agent, string(before), kind.recoveredTrigger)
	}
	return true, nil
}

func (s *service) ensureTurnStartedLocked(
	ctx context.Context,
	agent *agentState,
	trigger agentdto.AgentTrigger,
	states ...agentdto.AgentState,
) error {
	if agent == nil {
		return formatIllegalTransitionError(ctx, agent, "", string(trigger), errIllegalStateTransition)
	}
	if agent.state == agentdto.StateTurnStarting {
		return s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted)
	}
	if agentStateMatches(agent.state, states...) {
		return nil
	}
	return formatIllegalTransitionError(ctx, agent, string(agent.state), string(trigger), errIllegalStateTransition)
}

func agentStateMatches(state agentdto.AgentState, states ...agentdto.AgentState) bool {
	return slices.Contains(states, state)
}

const (
	maxProcessExitAutoRecoveries = 3
	processExitAutoRecoverWindow = 2 * time.Minute
)

func shouldAutoRecoverProcessExitLocked(s *service, agent *agentRuntime, err error) bool {
	if !processExitAutoRecoverable(s, agent, err) {
		return false
	}
	resetProcessExitAutoRecoverWindowLocked(agent, time.Now())
	if agent.autoRecoverCount >= maxProcessExitAutoRecoveries {
		agent.lastError = shared.FirstTrimmed(agent.lastError, err.Error()) + "; auto recovery retry limit reached"
		return false
	}
	agent.autoRecoverCount++
	return true
}

// processExitAutoRecoverable 判断进程退出后是否还能由本地命令或 launcher 恢复。
func processExitAutoRecoverable(s *service, agent *agentRuntime, err error) bool {
	return s != nil && agent != nil && err != nil && !agent.stopRequested &&
		(len(agent.command) > 0 || s.launcher != nil && agent.cmd == nil && strings.TrimSpace(agent.remoteThreadID) != "")
}

func resetProcessExitAutoRecoverWindowLocked(agent *agentRuntime, now time.Time) {
	if agent.autoRecoverSince.IsZero() || now.Sub(agent.autoRecoverSince) > processExitAutoRecoverWindow {
		agent.autoRecoverSince, agent.autoRecoverCount = now, 0
	}
}

func clearAgentAutoRecoveryLocked(agent *agentRuntime) {
	agent.autoRecoverCount, agent.autoRecoverSince = 0, time.Time{}
}

func shouldRecoverViaLauncher(ctx context.Context, s *service, agent *agentRuntime) bool {
	return s != nil && s.launcher != nil && agent != nil && agent.cmd == nil && s.launcher.IsRunning(ctx, agent)
}

func launchOwnsHookThreadBinding(state agentdto.AgentState) bool {
	return state == agentdto.StateProvisioning || state == agentdto.StateRecovering
}

func resetRuntimeAfterProcessExitLocked(agent *agentRuntime, recoverViaLauncher bool) {
	if !recoverViaLauncher {
		resetRuntimeStateLocked(agent)
	}
}

func (s *service) setProcessExitFallbackReportLocked(ctx context.Context, agent *agentRuntime, launchSeq uint64, shouldRecover bool) {
	if shouldRecover {
		return
	}
	if fallbackErr := s.setNoReportFallbackLocked(ctx, agent); fallbackErr != nil {
		s.logger.Warn("orchestration: process exit fallback report persist failed",
			"agent_id", agent.id, "launch_seq", launchSeq, "error", fallbackErr)
	}
}

func (s *service) recoverAfterProcessExit(ctx context.Context, agentID string, launchSeq uint64, shouldRecover bool) {
	if !shouldRecover {
		return
	}
	if recoverErr := s.recoverWithReason(ctx, agentID, recoverReasonProcessExit); recoverErr != nil {
		s.logger.Warn("orchestration: process exit recovery failed",
			"agent_id", agentID, "launch_seq", launchSeq, "error", recoverErr)
		if notifyErr := s.notifyRecoveryFailure(ctx, agentID, recoverErr); notifyErr != nil {
			s.logger.Warn("orchestration: recovery failure report notification failed",
				"agent_id", agentID, "launch_seq", launchSeq, "error", notifyErr)
		}
	}
}

func recoveryLaunchRequest(agent *agentRuntime) LaunchRequest {
	return LaunchRequest{
		AgentID: agent.id, Name: strings.TrimSpace(agent.name), Prompt: agent.prompt,
		Instructions: agent.instructions, ParentID: strings.TrimSpace(agent.parentID),
		AgentType: strings.TrimSpace(agent.agentType), AgentKey: strings.TrimSpace(agent.agentKey),
		MemoryScope: strings.TrimSpace(agent.memoryScope), Cwd: strings.TrimSpace(agent.cwd),
		Language: strings.TrimSpace(agent.language), Command: append([]string(nil), agent.command...),
		Env: append([]string(nil), agent.env...),
	}
}

// agentIdentityKind separates persisted-id API lookups from hook-only reverse lookups.
type agentIdentityKind int

const (
	agentIdentityLocalOnly agentIdentityKind = iota
	agentIdentityAny
)

// lookupAgentByIDLocked keeps reverse-capable lookup for trusted hook/event ingestion paths.
func lookupAgentByIDLocked(agents map[string]*agentState, agentID string) (*agentState, error) {
	return lookupAgentByIdentityLocked(agents, agentID, agentIdentityAny)
}

// lookupAgentByIdentityLocked 按调用方声明的信任范围查找 agent。
func lookupAgentByIdentityLocked(agents map[string]*agentState, agentID string, kind agentIdentityKind) (*agentState, error) {
	agentID = strings.TrimSpace(agentID)
	if agent, ok := agents[agentID]; ok {
		return agent, nil
	}
	if kind == agentIdentityLocalOnly {
		return nil, fmt.Errorf("%w: %s", errAgentNotFound, agentID)
	}
	for _, candidate := range agents {
		if candidate.remoteAgentID == agentID || candidate.remoteThreadID == agentID {
			return candidate, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", errAgentNotFound, agentID)
}

func applyLaunchRequestLocked(agent *agentRuntime, req LaunchRequest) {
	agent.requestedAgentID, agent.name = req.AgentID, managedAgentLaunchDisplayName(req.Name)
	agent.prompt, agent.instructions, agent.parentID = req.Prompt, req.Instructions, req.ParentID
	agent.agentType, agent.agentKey, agent.memoryScope = req.AgentType, req.AgentKey, req.MemoryScope
	agent.language, agent.cwd = req.Language, req.Cwd
	agent.command, agent.env = append([]string(nil), req.Command...), append([]string(nil), req.Env...)
	agent.port = launchPort(req)
	agent.portSource = ""
	if agent.port > 0 {
		agent.portSource = "inferred"
	}
	agent.provider = launchProvider(req)
	agent.providerSource = "inferred"
}

func (s *service) setNoReportFallbackLocked(ctx context.Context, agent *agentRuntime) error {
	if agent == nil || strings.TrimSpace(agent.lastReport) != "" {
		return nil
	}
	setReportLocked(ctx, agent, noReportFallbackText(string(agent.state), agent.lastError))
	if err := s.persistAgentReportFileAndGC(ctx, agentReportFileRecordFromRuntime(agent)); err != nil {
		return err
	}
	drainReportRequestersLocked(ctx, agent)
	return nil
}

// applyReportEventLocked 应用 report 事件，并在终态缺报告时生成兜底说明。
func (s *service) applyReportEventLocked(ctx context.Context, agent *agentRuntime, eventType string, data json.RawMessage, report string) (ReportEventResult, error) {
	terminal := isTerminalReportEvent(eventType, data)
	if report == "" && terminal && strings.TrimSpace(agent.lastReport) == "" {
		report = noReportFallbackText(string(agent.state), agent.lastError)
	}
	if report != "" {
		setReportLocked(ctx, agent, report)
		if err := s.persistAgentReportFileAndGC(ctx, agentReportFileRecordFromRuntime(agent)); err != nil {
			return ReportEventResult{}, err
		}
	}
	if report == "" {
		report = strings.TrimSpace(agent.lastReport)
	}
	notified := []string(nil)
	if report != "" || terminal {
		notified = drainReportRequestersLocked(ctx, agent)
	}
	return ReportEventResult{
		Success:              true,
		AgentID:              agent.id,
		EventType:            eventType,
		Report:               report,
		ReportSeq:            agent.lastReportSeq,
		UpdatedAt:            agent.lastReportUpdatedAt,
		NotifiedRequesterIDs: notified,
	}, nil
}

func noReportFallbackText(state, detail string) string {
	state, detail = strings.TrimSpace(state), strings.TrimSpace(detail)
	text := "agent ended"
	if state != "" {
		text += " in state '" + state + "'"
	}
	text += " without producing a turn report"
	if detail != "" {
		text += ": " + detail
	}
	return text
}

func bindHookThreadLocked(agent *agentRuntime, threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	if staleHookThread(agent, threadID) {
		return false
	}
	if threadID != "" {
		agent.threadID, agent.remoteThreadID = threadID, threadID
	}
	return true
}

func clearTerminalActiveTurnLocked(agent *agentRuntime, nextState string) {
	if agent != nil && terminalMirroredState(nextState) {
		agent.activeTurnID = ""
	}
}

func terminalMirroredState(nextState string) bool {
	return nextState == string(agentdto.StateIdle) ||
		nextState == string(agentdto.StateStopped) ||
		nextState == string(agentdto.StateFailed)
}

func (s *service) setStateChangedFallbackReportLocked(ctx context.Context, agent *agentRuntime, nextState string) {
	if agent == nil || !terminalFailedOrStopped(agent.state) || !terminalFailedOrStoppedString(nextState) {
		return
	}
	if err := s.setNoReportFallbackLocked(ctx, agent); err != nil {
		s.logger.Warn("orchestration: state-change fallback report persist failed",
			"agent_id", agent.id, "thread_id", agent.remoteThreadID, "state", nextState, "error", err)
	}
}

func terminalFailedOrStopped(state agentdto.AgentState) bool {
	return state == agentdto.StateFailed || state == agentdto.StateStopped
}

func terminalFailedOrStoppedString(state string) bool {
	return state == string(agentdto.StateFailed) || state == string(agentdto.StateStopped)
}

const pendingLaunchThreadConflict = "\x00conflict"

type stoppedHookSuppression struct {
	beforeOrAt time.Time
	permanent  bool
}

func (s *service) syncStateChangedHookLocked(ctx context.Context, agent *agentRuntime, nextState string) error {
	if nextState == string(agentdto.StateStopped) {
		return s.hookSyncForceStoppedLocked(ctx, agent)
	}
	return s.hookSyncFireLocked(ctx, agent, nextState)
}

func (s *service) suppressStoppedHookThreadLocked(threadID string) {
	if threadID = strings.TrimSpace(threadID); threadID != "" {
		s.suppressedStoppedThreads.Store(threadID, stoppedHookSuppression{permanent: true})
	}
}

func (s *service) suppressStoppedHookThreadUntilLocked(threadID string, beforeOrAt time.Time) {
	if threadID = strings.TrimSpace(threadID); threadID != "" {
		s.suppressedStoppedThreads.Store(threadID, stoppedHookSuppression{beforeOrAt: beforeOrAt})
	}
}

func (s *service) stoppedHookThreadSuppressed(threadID string, timestamp time.Time) bool {
	raw, ok := s.suppressedStoppedThreads.Load(strings.TrimSpace(threadID))
	if !ok {
		return false
	}
	suppression, ok := raw.(stoppedHookSuppression)
	if !ok || suppression.permanent {
		return true
	}
	return !timestamp.IsZero() && !timestamp.After(suppression.beforeOrAt)
}

func bindStateChangedHookThreadLocked(agent *agentRuntime, threadID, nextState string) bool {
	threadID = strings.TrimSpace(threadID)
	if recoveringOldThreadHook(agent, threadID) {
		return false
	}
	if recoveringNewTerminalThreadHook(agent, threadID, nextState) {
		return bindPendingLaunchThreadLocked(agent, threadID)
	}
	return bindHookThreadLocked(agent, threadID)
}

// recoveringNewTerminalThreadHook 判断恢复中的新线程终态是否属于本次 pending launch。
func recoveringNewTerminalThreadHook(agent *agentRuntime, threadID, nextState string) bool {
	return agent != nil && agent.state == agentdto.StateRecovering &&
		strings.TrimSpace(threadID) != "" && strings.TrimSpace(threadID) != strings.TrimSpace(agent.remoteThreadID) &&
		terminalFailedOrStoppedString(nextState) &&
		(pendingLaunchThreadMatches(agent, threadID) || pendingLaunchThreadConflicted(agent))
}

func recoveringOldThreadHook(agent *agentRuntime, threadID string) bool {
	return agent != nil && agent.state == agentdto.StateRecovering &&
		strings.TrimSpace(threadID) != "" && strings.TrimSpace(threadID) == strings.TrimSpace(agent.remoteThreadID)
}

// bindStoppedHookThreadLocked 处理 stopped hook 的线程绑定，避免覆盖已停止 agent。
func bindStoppedHookThreadLocked(agent *agentRuntime, threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	if agent != nil && agent.state == agentdto.StateStopped {
		return false
	}
	if agent != nil && agent.state == agentdto.StateRecovering &&
		threadID != "" && threadID != strings.TrimSpace(agent.remoteThreadID) {
		return bindPendingLaunchThreadLocked(agent, threadID)
	}
	return bindHookThreadLocked(agent, threadID)
}

// recordPendingLaunchThreadLocked 记录启动阶段看到的新远端线程，供恢复判定使用。
func recordPendingLaunchThreadLocked(agent *agentRuntime, threadID string, eventTime time.Time) {
	threadID = strings.TrimSpace(threadID)
	if agent == nil || threadID == "" || !launchOwnsHookThreadBinding(agent.state) ||
		recoveringOldThreadHook(agent, threadID) || stalePendingLaunchStartedEvent(agent, eventTime) {
		return
	}
	if pending := strings.TrimSpace(agent.pendingLaunchThreadID); pending != "" {
		if pending != pendingLaunchThreadConflict && pending != threadID {
			agent.pendingLaunchThreadID, agent.pendingLaunchThreadAt = pendingLaunchThreadConflict, eventTime
		}
		return
	}
	agent.pendingLaunchThreadID, agent.pendingLaunchThreadAt = threadID, eventTime
}

func stalePendingLaunchStartedEvent(agent *agentRuntime, eventTime time.Time) bool {
	return eventTime.IsZero() || !agent.updatedAt.IsZero() && eventTime.Before(agent.updatedAt)
}

func pendingLaunchThreadMatches(agent *agentRuntime, threadID string) bool {
	return agent != nil && strings.TrimSpace(agent.pendingLaunchThreadID) != "" && strings.TrimSpace(threadID) == strings.TrimSpace(agent.pendingLaunchThreadID)
}

func pendingLaunchThreadConflicted(agent *agentRuntime) bool {
	return agent != nil && strings.TrimSpace(agent.pendingLaunchThreadID) == pendingLaunchThreadConflict
}

func bindPendingLaunchThreadLocked(agent *agentRuntime, threadID string) bool {
	if !pendingLaunchThreadMatches(agent, threadID) && !pendingLaunchThreadConflicted(agent) {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	agent.threadID, agent.remoteThreadID = threadID, threadID
	agent.pendingLaunchThreadID, agent.pendingLaunchThreadAt = "", time.Time{}
	return true
}

func (s *service) setStoppedFallbackReportLocked(ctx context.Context, agent *agentRuntime) {
	if err := s.setNoReportFallbackLocked(ctx, agent); err != nil {
		s.logger.Warn("orchestration: stopped hook fallback report persist failed",
			"agent_id", agent.id, "thread_id", agent.remoteThreadID, "error", err)
	}
}

func staleHookThread(agent *agentRuntime, threadID string) bool {
	return agent != nil && threadID != "" && strings.TrimSpace(agent.remoteThreadID) != "" &&
		threadID != strings.TrimSpace(agent.remoteThreadID)
}
