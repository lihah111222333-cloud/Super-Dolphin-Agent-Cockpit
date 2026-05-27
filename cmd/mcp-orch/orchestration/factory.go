package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

func resetLaunchState(agent *agentState) {
	if agent == nil {
		return
	}
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

func (s *service) setNoReportFallbackLocked(ctx context.Context, agent *agentRuntime) error {
	if agent == nil || strings.TrimSpace(agent.lastReport) != "" {
		return nil
	}
	setReportLocked(ctx, agent, noReportFallbackText(string(agent.state), agent.lastError))
	if err := persistAgentReportFile(agentReportFileRecordFromRuntime(agent)); err != nil {
		return err
	}
	drainReportRequestersLocked(ctx, agent)
	return nil
}

func (s *service) applyReportEventLocked(ctx context.Context, agent *agentRuntime, eventType string, data json.RawMessage, report string) (ReportEventResult, error) {
	terminal := isTerminalReportEvent(eventType, data)
	if report == "" && terminal && strings.TrimSpace(agent.lastReport) == "" {
		report = noReportFallbackText(string(agent.state), agent.lastError)
	}
	if report != "" {
		setReportLocked(ctx, agent, report)
		if err := persistAgentReportFile(agentReportFileRecordFromRuntime(agent)); err != nil {
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
	if err := ctx.Err(); err != nil {
		return err
	}
	for !s.mu.TryRLock() {
		if err := ctx.Err(); err != nil {
			return err
		}
		time.Sleep(time.Millisecond)
	}
	return nil
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

// lookupAgentByIdentityLocked resolves an agent handle under the declared trust domain.
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
