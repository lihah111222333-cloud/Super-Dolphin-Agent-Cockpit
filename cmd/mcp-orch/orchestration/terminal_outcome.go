package orchestration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

var errTerminalProjectionNotReady = errors.New("terminal outcome projection runtime is not ready")

// CommitTurnCompleted 将 provider 终态提交到 durable public SSOT，再由 projector 单向更新 runtime。
func (s *service) CommitTurnCompleted(ctx context.Context, ev turndto.TurnCompleted) (bool, error) {
	if s == nil || s.terminalOutcomes == nil {
		return false, nil
	}
	err := s.registry.withAgentLocked(ev.AgentID, func(agent *agentRuntime) error {
		return s.commitTurnCompletedLocked(ctx, agent, ev)
	})
	return true, err
}

func (s *service) commitTurnCompletedLocked(ctx context.Context, agent *agentRuntime, ev turndto.TurnCompleted) error {
	if strings.TrimSpace(agent.activeTurnID) == "" {
		return s.validateTurnCompletedReplay(ctx, ev)
	}
	commit, err := terminalOutcomeCommitFromEvent(agent, ev)
	if err != nil {
		return err
	}
	_, err = s.terminalOutcomes.CommitTerminalOutcome(ctx, commit)
	if err != nil {
		return err
	}
	return nil
}

func (s *service) validateTurnCompletedReplay(ctx context.Context, ev turndto.TurnCompleted) error {
	existing, err := s.terminalOutcomes.GetPublicTerminalOutcome(ctx, strings.TrimSpace(ev.AgentID))
	if err != nil {
		return err
	}
	_, _, eventID, err := publicOutcomeFromTurnCompleted(ev)
	if err != nil {
		return err
	}
	if terminalReplayMatchesEvent(existing, ev, eventID) {
		return nil
	}
	return contract.ErrTerminalOutcomeConflict
}

func terminalReplayMatchesEvent(existing contract.TerminalOutcomeCommit, ev turndto.TurnCompleted, eventID string) bool {
	return existing.Identity.EventID == eventID &&
		existing.Identity.PublicThreadID == strings.TrimSpace(ev.ThreadID) &&
		existing.Identity.ProviderTurnID == strings.TrimSpace(ev.TurnID)
}

// CommitTurnInterrupted 将 legacy interruption 适配为固定安全文案的 canonical terminal commit。
func (s *service) CommitTurnInterrupted(ctx context.Context, ev turndto.TurnInterrupted) (bool, error) {
	return s.CommitTurnCompleted(ctx, turndto.TurnCompleted{
		TurnHeader: ev.TurnHeader, Success: false, Status: "interrupted", Reason: ev.Reason,
	})
}

// CommitStateChangedTerminal 让 failed/stopped StateChanged 与 TurnCompleted 复用同一 durable port/fence。
func (s *service) CommitStateChangedTerminal(ctx context.Context, ev agentdto.StateChanged) (bool, error) {
	projectionKind := ""
	switch strings.TrimSpace(ev.NewState) {
	case string(agentdto.StateFailed):
		projectionKind = "agent_failed"
	case string(agentdto.StateStopped):
		projectionKind = "agent_stopped"
	default:
		return false, nil
	}
	return s.commitAgentTerminal(ctx, ev.AgentID, ev.ThreadID, ev.SessionID, ev.Timestamp, projectionKind)
}

// CommitThreadStoppedTerminal 将 stopped hook 适配成显式 session/generation v2 terminal identity。
func (s *service) CommitThreadStoppedTerminal(ctx context.Context, ev threaddto.Stopped) (bool, error) {
	if s == nil || s.terminalOutcomes == nil {
		return false, nil
	}
	return true, errors.New("legacy thread stopped event lacks authenticated session and generation for canonical terminal v2")
}

// CommitRuntimeLossTerminal 隔离缺少 session/generation 的 legacy reportEvent，禁止绑定当前 runtime。
func (s *service) CommitRuntimeLossTerminal(ctx context.Context, agentID, eventType string) (bool, error) {
	if s == nil || s.terminalOutcomes == nil {
		return false, nil
	}
	return true, fmt.Errorf("legacy runtime-loss event %q for agent %q lacks authenticated session and generation for canonical terminal v2",
		strings.TrimSpace(eventType), strings.TrimSpace(agentID))
}

// commitAgentTerminal 在同一 agent lock 内完成终态 fence 和 DB commit；可见状态只由 outbox projector 更新。
func (s *service) commitAgentTerminal(ctx context.Context, agentID, threadID, eventSessionID string, occurredAt time.Time, projectionKind string) (bool, error) {
	if s == nil || s.terminalOutcomes == nil {
		return false, nil
	}
	err := s.registry.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if terminalFailedOrStopped(agent.state) {
			existing, err := s.terminalOutcomes.GetPublicTerminalOutcome(ctx, strings.TrimSpace(agentID))
			if err != nil {
				return err
			}
			if publicTerminalMatchesAgent(existing, agent, threadID) {
				return nil
			}
			return contract.ErrTerminalOutcomeConflict
		}
		commit, err := terminalOutcomeCommitFromAgentTerminal(agent, threadID, eventSessionID, occurredAt, projectionKind)
		if err != nil {
			return err
		}
		_, err = s.terminalOutcomes.CommitTerminalOutcome(ctx, commit)
		if err != nil {
			return err
		}
		return nil
	})
	return true, err
}

func publicTerminalMatchesAgent(outcome contract.TerminalOutcomeCommit, agent *agentRuntime, threadID string) bool {
	if agent == nil {
		return false
	}
	return outcome.Identity.AgentID == strings.TrimSpace(agent.id) &&
		outcome.Identity.PublicThreadID == strings.TrimSpace(threadID) &&
		outcome.Identity.SessionID == agentSessionID(agent) &&
		outcome.Identity.Generation == agent.sessionGeneration
}

func terminalOutcomeCommitFromAgentTerminal(agent *agentRuntime, threadID, eventSessionID string, occurredAt time.Time, projectionKind string) (contract.TerminalOutcomeCommit, error) {
	threadID, sessionID, err := validateAgentTerminalFence(agent, threadID, eventSessionID, occurredAt, projectionKind)
	if err != nil {
		return contract.TerminalOutcomeCommit{}, err
	}
	providerTurnID := strings.TrimSpace(agent.activeTurnID)
	if providerTurnID == "" {
		providerTurnID = "session-terminal:" + sessionID
	}
	code, kind, title, message := agentTerminalPublicFields(projectionKind)
	eventID := agentTerminalEventID(agent.id, threadID, providerTurnID, sessionID, agent.sessionGeneration, projectionKind, occurredAt)
	publicError := canonicalAgentPublicError(eventID, code, title, message)
	report := fmt.Sprintf("agent %s: %s (diagnostic id: %s)", kind, publicError.Message, publicError.DiagnosticID)
	identity := contract.CanonicalTerminalIdentity{
		Capability: contract.TerminalOutcomeCapabilityV2, AgentID: agent.id,
		PublicThreadID: threadID, ProviderTurnID: providerTurnID, SessionID: sessionID,
		Generation: agent.sessionGeneration, EventID: eventID,
		TerminalIdentity:    terminalIdentity(eventID, agent.id, threadID, providerTurnID, sessionID, agent.sessionGeneration),
		ExpectedActiveState: string(agent.state),
		HeadVersion:         agent.terminalHeadVersion,
	}
	return contract.TerminalOutcomeCommit{
		SchemaVersion: 2, ProjectionKind: projectionKind, Identity: identity,
		PublicOutcome: contract.PublicOutcome{
			Kind: kind, Code: code,
			PublicError: publicError, CompletedAt: occurredAt.UTC(),
		},
		PublicReport: report, OccurredAt: occurredAt.UTC(),
	}, nil
}

func validateAgentTerminalFence(agent *agentRuntime, threadID, eventSessionID string, occurredAt time.Time, projectionKind string) (string, string, error) {
	if agent == nil {
		return "", "", errAgentNotFound
	}
	if occurredAt.IsZero() {
		return "", "", errors.New("agent terminal occurredAt is required")
	}
	threadID, err := validateAgentTerminalThread(agent, threadID)
	if err != nil {
		return "", "", err
	}
	sessionID, err := validateAgentTerminalSession(agent, eventSessionID, projectionKind)
	if err != nil {
		return "", "", err
	}
	return threadID, sessionID, nil
}

func validateAgentTerminalThread(agent *agentRuntime, threadID string) (string, error) {
	threadID = strings.TrimSpace(threadID)
	currentThreadID := strings.TrimSpace(firstNonEmpty(agent.remoteThreadID, agent.threadID))
	if threadID == "" || threadID != currentThreadID {
		return "", fmt.Errorf("agent terminal public thread mismatch: event=%q current=%q", threadID, currentThreadID)
	}
	return threadID, nil
}

// validateAgentTerminalSession 拒绝空代际、空 state-event session 和跨会话终态。
func validateAgentTerminalSession(agent *agentRuntime, eventSessionID, projectionKind string) (string, error) {
	sessionID := agentSessionID(agent)
	if sessionID == "" || agent.sessionGeneration == 0 {
		return "", errors.New("agent terminal v2 requires explicit session id and generation")
	}
	if stateTerminalRequiresEventSession(projectionKind) && strings.TrimSpace(eventSessionID) == "" {
		return "", errors.New("agent terminal state event requires explicit session id")
	}
	if strings.TrimSpace(eventSessionID) != "" && strings.TrimSpace(eventSessionID) != sessionID {
		return "", fmt.Errorf("agent terminal session mismatch: event=%q current=%q", eventSessionID, sessionID)
	}
	return sessionID, nil
}

func stateTerminalRequiresEventSession(projectionKind string) bool {
	return projectionKind == "agent_failed" || projectionKind == "agent_stopped"
}

func agentTerminalPublicFields(projectionKind string) (code, kind, title, message string) {
	if projectionKind == "agent_stopped" || projectionKind == "process_stopped" {
		return "AGENT_STOPPED", "stopped", "Agent 已停止", "Agent 已安全停止。"
	}
	return "AGENT_FAILED", "failure", "Agent 执行失败", "Agent 未能完成本次执行。"
}

func canonicalAgentPublicError(eventID, code, title, message string) *turndto.PublicErrorV1 {
	digest := sha256.Sum256([]byte(eventID))
	return &turndto.PublicErrorV1{
		Code: code, Title: title, Message: message, DiagnosticID: fmt.Sprintf("diag-%x", digest),
		Retryable: false, RecoveryActions: []string{"copy_diagnostics"},
	}
}

func agentTerminalEventID(agentID, threadID, providerTurnID, sessionID string, generation uint64, projectionKind string, occurredAt time.Time) string {
	source := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d\x00%s\x00%s",
		agentID, threadID, providerTurnID, sessionID, generation, projectionKind, occurredAt.UTC().Format(time.RFC3339Nano))
	digest := sha256.Sum256([]byte(source))
	return fmt.Sprintf("agent-terminal:%x", digest)
}

// terminalOutcomeCommitFromEvent 把 provider TurnCompleted 映射为 v2 durable public commit。
func terminalOutcomeCommitFromEvent(agent *agentRuntime, ev turndto.TurnCompleted) (contract.TerminalOutcomeCommit, error) {
	threadID, providerTurnID, sessionID, err := terminalTurnEventFence(agent, ev)
	if err != nil {
		return contract.TerminalOutcomeCommit{}, err
	}
	publicOutcome, report, eventID, err := publicOutcomeFromTurnCompleted(ev)
	if err != nil {
		return contract.TerminalOutcomeCommit{}, err
	}
	identity := canonicalTerminalIdentityForEvent(agent, threadID, providerTurnID, sessionID, eventID)
	commit := contract.TerminalOutcomeCommit{
		SchemaVersion: 2, ProjectionKind: "turn_completed", Identity: identity, PublicOutcome: publicOutcome,
		PublicReport: report, OccurredAt: ev.Timestamp.UTC(),
	}
	if result := strings.TrimSpace(ev.Result); result != "" {
		commit.PrivateDAG = &contract.OwnerScopedDAGPayload{
			OwnerAgentID: identity.AgentID, PublicThreadID: identity.PublicThreadID,
			ProviderTurnID: identity.ProviderTurnID, Result: result,
		}
	}
	return commit, nil
}

func terminalTurnEventFence(agent *agentRuntime, ev turndto.TurnCompleted) (string, string, string, error) {
	if agent == nil {
		return "", "", "", errAgentNotFound
	}
	if ev.Timestamp.IsZero() {
		return "", "", "", errors.New("terminal outcome event timestamp is required")
	}
	threadID := strings.TrimSpace(ev.ThreadID)
	currentThreadID := strings.TrimSpace(firstNonEmpty(agent.remoteThreadID, agent.threadID))
	if threadID == "" || currentThreadID == "" || threadID != currentThreadID {
		return "", "", "", fmt.Errorf("terminal outcome public thread mismatch: event=%q current=%q", threadID, currentThreadID)
	}
	providerTurnID, ok := canonicalProviderTurnID(agent, ev.TurnID)
	if !ok {
		return "", "", "", fmt.Errorf("terminal outcome provider turn mismatch: event=%q active=%q", ev.TurnID, agent.activeTurnID)
	}
	sessionID := agentSessionID(agent)
	if sessionID == "" || agent.sessionGeneration == 0 {
		return "", "", "", errors.New("terminal outcome v2 requires explicit session id and generation")
	}
	return threadID, providerTurnID, sessionID, nil
}

func canonicalTerminalIdentityForEvent(agent *agentRuntime, threadID, providerTurnID, sessionID, eventID string) contract.CanonicalTerminalIdentity {
	return contract.CanonicalTerminalIdentity{
		Capability: contract.TerminalOutcomeCapabilityV2, AgentID: strings.TrimSpace(agent.id),
		PublicThreadID: threadID, ProviderTurnID: providerTurnID,
		SessionID: sessionID, Generation: agent.sessionGeneration, EventID: eventID,
		TerminalIdentity:    terminalIdentity(eventID, agent.id, threadID, providerTurnID, sessionID, agent.sessionGeneration),
		ExpectedActiveState: string(agent.state),
		HeadVersion:         agent.terminalHeadVersion,
	}
}

// canonicalProviderTurnID 只接受 active turn 或已登记的 provider turn alias。
func canonicalProviderTurnID(agent *agentRuntime, eventTurnID string) (string, bool) {
	eventTurnID = strings.TrimSpace(eventTurnID)
	active := strings.TrimSpace(agent.activeTurnID)
	alias := agent.providerTurnAlias
	if eventTurnID == "" || active == "" {
		return "", false
	}
	if eventTurnID == active {
		return eventTurnID, true
	}
	if eventTurnID == strings.TrimSpace(alias.providerTurnID) && active == strings.TrimSpace(alias.localTurnID) {
		return eventTurnID, true
	}
	return "", false
}

// publicOutcomeFromTurnCompleted 仅迁移明确 public-safe 的成功摘要或 canonical PublicError。
func publicOutcomeFromTurnCompleted(ev turndto.TurnCompleted) (contract.PublicOutcome, string, string, error) {
	terminal, canonical, err := turndto.CanonicalTurnTerminal(ev)
	if err != nil {
		return contract.PublicOutcome{}, "", "", err
	}
	eventID := ""
	if canonical {
		eventID = strings.TrimSpace(terminal.EventID)
	}
	if eventID == "" {
		eventID = localTerminalEventID(ev)
	}
	completedAt := ev.Timestamp.UTC()
	if ev.Success {
		if !canonical {
			return contract.PublicOutcome{}, "", "", errors.New("successful terminal outcome requires canonical v2 public summary")
		}
		summary := strings.TrimSpace(terminal.PublicSummary)
		if summary == "" {
			return contract.PublicOutcome{}, "", "", errors.New("successful terminal outcome requires explicit public-safe summary")
		}
		return contract.PublicOutcome{
			Kind: "success", Code: terminalOutcomeCode(ev, terminal), Summary: summary, CompletedAt: completedAt,
		}, summary, eventID, nil
	}
	publicError := canonicalPublicError(eventID, terminal, canonical)
	kind := "failure"
	if terminalWasStopped(ev) {
		kind = "stopped"
	}
	report := fmt.Sprintf("turn %s: %s (diagnostic id: %s)", kind, publicError.Message, publicError.DiagnosticID)
	return contract.PublicOutcome{
		Kind: kind, Code: terminalOutcomeCode(ev, terminal), PublicError: publicError, CompletedAt: completedAt,
	}, report, eventID, nil
}

func canonicalPublicError(eventID string, terminal turndto.TurnTerminalV2, canonical bool) *turndto.PublicErrorV1 {
	if canonical && terminal.PublicError != nil {
		value := *terminal.PublicError
		value.RecoveryActions = append([]string(nil), terminal.PublicError.RecoveryActions...)
		return &value
	}
	digest := sha256.Sum256([]byte(eventID))
	return &turndto.PublicErrorV1{
		Code: "TERMINAL_OUTCOME_FAILED", Title: "本次执行未完成",
		Message: "Agent 未能完成本次执行。", DiagnosticID: fmt.Sprintf("diag-%x", digest),
		Retryable: false, RecoveryActions: []string{"copy_diagnostics"},
	}
}

func terminalOutcomeCode(ev turndto.TurnCompleted, terminal turndto.TurnTerminalV2) string {
	if strings.TrimSpace(terminal.Outcome) != "" {
		return strings.TrimSpace(terminal.Outcome)
	}
	if strings.TrimSpace(ev.Status) != "" {
		return strings.TrimSpace(ev.Status)
	}
	if ev.Success {
		return "success"
	}
	return "failed"
}

func localTerminalEventID(ev turndto.TurnCompleted) string {
	source := strings.Join([]string{
		strings.TrimSpace(ev.AgentID), strings.TrimSpace(ev.ThreadID), strings.TrimSpace(ev.TurnID),
		ev.Timestamp.UTC().Format(time.RFC3339Nano), terminalOutcomeCode(ev, turndto.TurnTerminalV2{}),
	}, "\x00")
	digest := sha256.Sum256([]byte(source))
	return fmt.Sprintf("terminal:%x", digest)
}

func terminalIdentity(eventID, agentID, threadID, turnID, sessionID string, generation uint64) string {
	source := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%d", eventID, agentID, threadID, turnID, sessionID, generation)
	digest := sha256.Sum256([]byte(source))
	return fmt.Sprintf("terminal-identity:%x", digest)
}

// projectTerminalOutcomeLocked 是 durable commit 到 runtime/report/event 的唯一投影器。
func (s *service) projectTerminalOutcomeLocked(ctx context.Context, agent *agentRuntime, commit contract.TerminalOutcomeCommit) error {
	if terminalProjectionAlreadyApplied(agent, commit) {
		return nil
	}
	if err := validateTerminalProjectionIdentity(agent, commit.Identity); err != nil {
		return err
	}
	if strings.TrimSpace(agent.activeTurnID) == "" && commit.ProjectionKind == "turn_completed" {
		return errTerminalProjectionNotReady
	}
	if commit.ProjectionKind != "turn_completed" {
		return s.projectAgentTerminalOutcomeLocked(ctx, agent, commit)
	}
	trigger := agentdto.TriggerTurnAborted
	clearError := false
	errorText := commit.PublicReport
	if commit.PublicOutcome.Kind == "success" {
		trigger = agentdto.TriggerTurnCompleted
		clearError = true
		errorText = ""
	}
	if err := s.finalizeActiveTurnLocked(ctx, agent, agent.activeTurnID, activeTurnFinalizationKind{
		trigger: trigger, clearError: clearError, errorText: errorText,
	}); err != nil {
		return err
	}
	setReportLocked(ctx, agent, commit.PublicReport)
	agent.outcome = agentOutcomeFromPublic(commit.PublicOutcome)
	drainReportRequestersLocked(ctx, agent)
	return nil
}

// validateTerminalProjectionIdentity 在投影前复核 agent/thread/session/generation fence。
func validateTerminalProjectionIdentity(agent *agentRuntime, identity contract.CanonicalTerminalIdentity) error {
	if agent == nil {
		return errTerminalProjectionNotReady
	}
	if strings.TrimSpace(agent.id) != identity.AgentID {
		return errTerminalProjectionNotReady
	}
	if strings.TrimSpace(firstNonEmpty(agent.remoteThreadID, agent.threadID)) != identity.PublicThreadID {
		return errTerminalProjectionNotReady
	}
	if agentSessionID(agent) != identity.SessionID || agent.sessionGeneration != identity.Generation {
		return errTerminalProjectionNotReady
	}
	if agent.terminalHeadVersion != identity.HeadVersion || string(agent.state) != identity.ExpectedActiveState {
		return errTerminalProjectionNotReady
	}
	if !terminalProjectionTurnMatches(agent, identity) {
		return errTerminalProjectionNotReady
	}
	return nil
}

func terminalProjectionTurnMatches(agent *agentRuntime, identity contract.CanonicalTerminalIdentity) bool {
	if strings.TrimSpace(agent.activeTurnID) == "" {
		return identity.ProviderTurnID == "session-terminal:"+identity.SessionID
	}
	_, ok := canonicalProviderTurnID(agent, identity.ProviderTurnID)
	return ok
}

func terminalProjectionAlreadyApplied(agent *agentRuntime, commit contract.TerminalOutcomeCommit) bool {
	if agent == nil || strings.TrimSpace(agent.activeTurnID) != "" || agent.outcome == nil {
		return false
	}
	expected := agentOutcomeFromPublic(commit.PublicOutcome)
	return terminalProjectionReplayFenceFromAgent(agent) == terminalProjectionReplayFenceFromIdentity(commit.Identity) &&
		string(agent.state) == publicTerminalState(commit) &&
		strings.TrimSpace(agent.lastReport) == strings.TrimSpace(commit.PublicReport) &&
		*agent.outcome == *expected
}

type terminalProjectionReplayFence struct {
	agentID, publicThreadID, sessionID string
	generation, headVersion            uint64
}

func terminalProjectionReplayFenceFromAgent(agent *agentRuntime) terminalProjectionReplayFence {
	return terminalProjectionReplayFence{
		agentID: strings.TrimSpace(agent.id), publicThreadID: strings.TrimSpace(firstNonEmpty(agent.remoteThreadID, agent.threadID)),
		sessionID: agentSessionID(agent), generation: agent.sessionGeneration, headVersion: agent.terminalHeadVersion,
	}
}

func terminalProjectionReplayFenceFromIdentity(identity contract.CanonicalTerminalIdentity) terminalProjectionReplayFence {
	return terminalProjectionReplayFence{
		agentID: identity.AgentID, publicThreadID: identity.PublicThreadID,
		sessionID: identity.SessionID, generation: identity.Generation, headVersion: identity.HeadVersion,
	}
}

func (s *service) projectAgentTerminalOutcomeLocked(ctx context.Context, agent *agentRuntime, commit contract.TerminalOutcomeCommit) error {
	target := string(agentdto.StateFailed)
	if commit.ProjectionKind == "agent_stopped" || commit.ProjectionKind == "process_stopped" {
		target = string(agentdto.StateStopped)
	}
	if err := s.syncStateChangedHookLocked(ctx, agent, target); err != nil {
		return err
	}
	agent.activeTurnID = ""
	agent.providerTurnAlias = providerTurnAlias{}
	setReportLocked(ctx, agent, commit.PublicReport)
	agent.outcome = agentOutcomeFromPublic(commit.PublicOutcome)
	drainReportRequestersLocked(ctx, agent)
	if target == string(agentdto.StateStopped) {
		emitEvent(s.eventBus, eventTypeAgentStopped, eventAgentID(agent), agent, commit.PublicReport)
	} else {
		emitEvent(s.eventBus, eventTypeAgentFailed, eventAgentID(agent), agent, commit.PublicReport, false)
	}
	return nil
}

// CommitProcessExitTerminalLocked 在 lifecycle 已持有 registry lock 时提交 public terminal；可见终态仍由 outbox projector 投影。
func (s *service) CommitProcessExitTerminalLocked(ctx context.Context, agent *agentRuntime, launchSeq uint64, processErr error) (bool, error) {
	if s == nil || s.terminalOutcomes == nil {
		return false, nil
	}
	if agent == nil || agent.launchSeq != launchSeq {
		return true, contract.ErrTerminalOutcomeConflict
	}
	if terminalFailedOrStopped(agent.state) {
		return true, s.validateExistingProcessTerminal(ctx, agent)
	}
	projectionKind := processTerminalProjectionKind(agent)
	commit, err := terminalOutcomeCommitFromAgentTerminal(
		agent, firstNonEmpty(agent.remoteThreadID, agent.threadID), agentSessionID(agent),
		resolveEventTime(ctx, agent.updatedAt, agent.startedAt), projectionKind,
	)
	if err != nil {
		return true, err
	}
	_, err = s.terminalOutcomes.CommitTerminalOutcome(ctx, commit)
	if err != nil {
		return true, err
	}
	_ = processErr
	return true, nil
}

func (s *service) validateExistingProcessTerminal(ctx context.Context, agent *agentRuntime) error {
	existing, err := s.terminalOutcomes.GetPublicTerminalOutcome(ctx, strings.TrimSpace(agent.id))
	if err != nil {
		return err
	}
	if publicTerminalMatchesAgent(existing, agent, firstNonEmpty(agent.remoteThreadID, agent.threadID)) {
		return nil
	}
	return contract.ErrTerminalOutcomeConflict
}

func processTerminalProjectionKind(agent *agentRuntime) string {
	if agent.stopRequested {
		return "process_stopped"
	}
	return "process_failed"
}

func agentOutcomeFromPublic(outcome contract.PublicOutcome) *agentdto.Outcome {
	value := &agentdto.Outcome{Code: outcome.Code, CompletedAt: outcome.CompletedAt}
	switch outcome.Kind {
	case "success":
		value.Kind, value.Summary = agentdto.OutcomeKindSuccess, outcome.Summary
	case "stopped":
		value.Kind = agentdto.OutcomeKindStopped
		if outcome.PublicError != nil {
			value.Reason = outcome.PublicError.Message
		}
	default:
		value.Kind = agentdto.OutcomeKindFailure
		if outcome.PublicError != nil {
			value.Reason = outcome.PublicError.Message
		}
	}
	return value
}

// overlayPublicTerminalOutcome 让 Board/Snapshot 冷读以 durable public record 覆盖 runtime 缓存。
func (s *service) overlayPublicTerminalOutcome(ctx context.Context, snapshot *AgentSnapshot) error {
	if s == nil || s.terminalOutcomes == nil || snapshot == nil {
		return nil
	}
	outcome, err := s.terminalOutcomes.GetPublicTerminalOutcome(ctx, strings.TrimSpace(snapshot.AgentID))
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, contract.ErrTerminalOutcomeActive) {
		return nil
	}
	if err != nil {
		return err
	}
	if outcome.Identity.AgentID != strings.TrimSpace(snapshot.AgentID) {
		return errors.New("public terminal outcome agent identity mismatch")
	}
	snapshot.LastReport = outcome.PublicReport
	snapshot.Outcome = agentOutcomeFromPublic(outcome.PublicOutcome)
	snapshot.UpdatedAt = outcome.OccurredAt
	switch outcome.PublicOutcome.Kind {
	case "success":
		snapshot.State = string(agentdto.StateIdle)
	case "stopped":
		snapshot.State = string(agentdto.StateStopped)
	default:
		snapshot.State = string(agentdto.StateFailed)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// terminalOutcomeProjector 从事务 outbox 恢复 commit 后、投影前的崩溃窗口。
type terminalOutcomeProjector struct {
	runtime  TerminalOutcomeProjectionRuntime
	logger   *slog.Logger
	workerID string
}

// TerminalOutcomeProjectionRuntime 是 outbox runner 可调用的窄投影边界。
type TerminalOutcomeProjectionRuntime interface {
	ProcessTerminalOutcomeOutbox(ctx context.Context, workerID string, lease time.Duration, limit int) error
}

// NewTerminalOutcomeProjector 创建受 RunGroup 托管的 durable terminal projector。
func NewTerminalOutcomeProjector(runtime TerminalOutcomeProjectionRuntime, logger *slog.Logger) (*terminalOutcomeProjector, error) {
	if runtime == nil {
		return nil, errors.New("terminal outcome projector requires projection runtime")
	}
	var token [12]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, fmt.Errorf("generate terminal outcome projector worker id: %w", err)
	}
	return &terminalOutcomeProjector{
		runtime: runtime, logger: loggerOrDefault(logger),
		workerID: "mcp-orch-terminal-projector-v2-" + hex.EncodeToString(token[:]),
	}, nil
}

// Run 周期领取 outbox；单条暂不可投影时保留 claim，lease 到期后重试。
func (p *terminalOutcomeProjector) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := p.runtime.ProcessTerminalOutcomeOutbox(ctx, p.workerID, 30*time.Second, 32); err != nil && ctx.Err() == nil {
			p.logger.Warn("orchestration: terminal outcome projector retry", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// ProcessTerminalOutcomeOutbox 领取、单向投影并按 worker fence 确认 durable outbox。
func (s *service) ProcessTerminalOutcomeOutbox(ctx context.Context, workerID string, lease time.Duration, limit int) error {
	if s == nil || s.terminalOutcomes == nil {
		return errors.New("terminal outcome projection store is not configured")
	}
	if lease < 6*time.Millisecond {
		return errors.New("terminal outcome projection lease must be at least 6ms")
	}
	items, err := s.terminalOutcomes.ClaimTerminalOutcomeOutbox(ctx, workerID, lease, limit)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := s.processTerminalOutcomeOutboxItem(ctx, workerID, lease, item); err != nil {
			return err
		}
	}
	return nil
}

// processTerminalOutcomeOutboxItem 在 runtime/DAG 投影期间持续续租，ACK 前等待 heartbeat 收束。
func (s *service) processTerminalOutcomeOutboxItem(ctx context.Context, workerID string, lease time.Duration, item contract.TerminalOutcomeOutboxItem) error {
	if _, err := s.terminalOutcomes.RenewTerminalOutcomeOutbox(ctx, item.ID, workerID, item.ClaimToken, lease); err != nil {
		return err
	}
	workCtx, cancelWork := context.WithCancel(ctx)
	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	heartbeatDone := s.startTerminalOutcomeHeartbeat(heartbeatCtx, cancelWork, workerID, lease, item)

	workErr := s.projectTerminalOutcomeOutboxItem(workCtx, item)
	cancelHeartbeat()
	heartbeatErr := <-heartbeatDone
	cancelWork()
	if err := errors.Join(workErr, heartbeatErr); err != nil {
		return err
	}
	return s.terminalOutcomes.MarkTerminalOutcomeProjected(ctx, item.ID, workerID, item.ClaimToken)
}

// startTerminalOutcomeHeartbeat 启动受统一 panic 恢复保护的续租，并保证所有退出路径都关闭完成通道。
func (s *service) startTerminalOutcomeHeartbeat(ctx context.Context, cancelWork context.CancelFunc, workerID string, lease time.Duration, item contract.TerminalOutcomeOutboxItem) <-chan error {
	done := make(chan error, 1)
	runtimesafe.SafeGo(ctx, s.logger, "orchestration.terminalOutcomeHeartbeat", func(context.Context) {
		heartbeatErr := errors.New("terminal outcome heartbeat panicked")
		defer func() {
			cancelWork()
			done <- heartbeatErr
			close(done)
		}()
		heartbeatErr = s.renewTerminalOutcomeLease(ctx, cancelWork, workerID, lease, item)
	})
	return done
}

// renewTerminalOutcomeLease 只用当前 worker/token 续租；fence 丢失时取消正在执行的投影。
func (s *service) renewTerminalOutcomeLease(ctx context.Context, cancelWork context.CancelFunc, workerID string, lease time.Duration, item contract.TerminalOutcomeOutboxItem) error {
	ticker := time.NewTicker(lease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := s.terminalOutcomes.RenewTerminalOutcomeOutbox(ctx, item.ID, workerID, item.ClaimToken, lease); err != nil {
				cancelWork()
				return err
			}
		}
	}
}

// projectTerminalOutcomeOutboxItem 执行公开 runtime 与 owner-scoped DAG 单向投影。
func (s *service) projectTerminalOutcomeOutboxItem(ctx context.Context, item contract.TerminalOutcomeOutboxItem) error {
	err := s.registry.withAgentLocked(item.Outcome.Identity.AgentID, func(agent *agentRuntime) error {
		return s.projectTerminalOutcomeLocked(ctx, agent, item.Outcome)
	})
	if err != nil && !errors.Is(err, errAgentNotFound) {
		return err
	}
	return s.projectTerminalOutcomeDAG(ctx, item)
}

// projectTerminalOutcomeDAG 在 outbox ACK 前同步推进 DAG；失败会保留 claim 等待 lease replay。
func (s *service) projectTerminalOutcomeDAG(ctx context.Context, item contract.TerminalOutcomeOutboxItem) error {
	if s == nil || s.terminalDAG == nil {
		return nil
	}
	deps := *s.terminalDAG
	if deps.LookupStore == nil && deps.FlowStore == nil {
		return nil
	}
	if deps.LookupStore == nil || deps.FlowStore == nil {
		return errors.New("terminal outcome DAG projection requires lookup and flow stores")
	}
	return projectDAGTurnCompleted(ctx, deps, loggerOrDefault(s.logger), publicTurnCompletedFromCommit(item.Outcome, item.PrivateDAG))
}

// publicTurnCompletedFromCommit 公开摘要与 owner-scoped 私有 DAG artifact 分型重建。
func publicTurnCompletedFromCommit(commit contract.TerminalOutcomeCommit, private *contract.OwnerScopedDAGPayload) turndto.TurnCompleted {
	success := commit.PublicOutcome.Kind == "success"
	reason := ""
	if !success {
		reason = strings.TrimSpace(commit.PublicOutcome.Code)
		if reason == "" {
			reason = "terminal_outcome_failure"
		}
	}
	result := ""
	if private != nil {
		result = strings.TrimSpace(private.Result)
	}
	return turndto.TurnCompleted{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader: shareddto.AgentHeader{
				ThreadHeader: shareddto.ThreadHeader{
					EventHeader: shareddto.EventHeader{Timestamp: commit.OccurredAt},
					ThreadID:    commit.Identity.PublicThreadID,
				},
				AgentID: commit.Identity.AgentID,
			},
			TurnIDHeader: shareddto.TurnIDHeader{TurnID: commit.Identity.ProviderTurnID},
		},
		Success: success,
		Status:  strings.TrimSpace(commit.PublicOutcome.Code),
		Reason:  reason,
		Result:  result,
		Summary: strings.TrimSpace(commit.PublicOutcome.Summary),
	}
}
