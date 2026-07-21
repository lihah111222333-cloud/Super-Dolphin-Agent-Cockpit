package orchestration

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"
	"go.uber.org/fx"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	shared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

type stubLifecycle struct {
	hooks []fx.Hook
}

type agentSnapshot struct {
	state        string
	activeTurnID string
	lastError    string
	updatedAt    time.Time
}

func (l *stubLifecycle) Append(hook fx.Hook) {
	l.hooks = append(l.hooks, hook)
}

func TestHandleTurnCompletedEventLogsSettlement(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	svc := NewService(logger, event.NewDispatcher(), nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.activeTurnID = "turn-1"
	svc.registry.agents[agent.id] = agent

	handleTurnCompletedEvent(svc, logger, completedEvent("agent-1", "thread-1", "turn-1", true, ""))

	output := buf.String()
	if !strings.Contains(output, "turn completed event received") {
		t.Fatalf("log output = %q, want completion receipt log", output)
	}
	if !strings.Contains(output, "turn completed event settled") {
		t.Fatalf("log output = %q, want completion settlement log", output)
	}
	if agent.state != agentdto.StateIdle {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateIdle)
	}
	if agent.activeTurnID != "" {
		t.Fatalf("activeTurnID = %q, want empty", agent.activeTurnID)
	}
}

func TestHandleTurnInterruptedEventLogsSettlement(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	svc := NewService(logger, event.NewDispatcher(), nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.activeTurnID = "turn-1"
	svc.registry.agents[agent.id] = agent

	handleTurnInterruptedEvent(svc, logger, interruptedEventAt("agent-1", "thread-1", "turn-1", "cancelled", time.Unix(1710000000, 0).UTC()))

	output := buf.String()
	if !strings.Contains(output, "turn interrupted event received") {
		t.Fatalf("log output = %q, want interruption receipt log", output)
	}
	if !strings.Contains(output, "turn interrupted event settled") {
		t.Fatalf("log output = %q, want interruption settlement log", output)
	}
	if agent.state != agentdto.StateIdle {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateIdle)
	}
	if agent.activeTurnID != "" {
		t.Fatalf("activeTurnID = %q, want empty", agent.activeTurnID)
	}
}

func TestForceIdleAfterCompletionErrorKeepsDifferentActiveTurn(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.activeTurnID = "turn-active"
	svc.registry.agents[agent.id] = agent

	recovered, err := svc.forceIdleAfterCompletionError(
		withEventTime(context.TODO(), time.Now()),
		"agent-1",
		"turn-finished",
		false,
		"boom",
	)
	if err == nil {
		t.Fatal("forceIdleAfterCompletionError() error = nil, want non-nil")
	}
	if recovered {
		t.Fatal("forceIdleAfterCompletionError() recovered = true, want false")
	}
	if agent.state != agentdto.StateTurnRunning {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateTurnRunning)
	}
	if agent.activeTurnID != "turn-active" {
		t.Fatalf("activeTurnID = %q, want turn-active", agent.activeTurnID)
	}
}

func TestHandleTurnCompletedEventSettlesProviderTurnIDMismatch(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "agent-1"
	localTurnID := "turn_1781685566961_2c3add7bb73076e1"
	providerTurnID := "019ed4bc-3f5d-74e3-a0c9-8c252c550c46"
	agent.activeTurnID = localTurnID
	agent.reportRequesters = []string{"parent-1"}
	svc.registry.agents[agent.id] = agent
	if err := svc.BindActiveTurnID(context.Background(), agent.id, providerTurnID); err != nil {
		t.Fatalf("BindActiveTurnID() error = %v", err)
	}
	if agent.activeTurnID != localTurnID {
		t.Fatalf("activeTurnID = %q, want local turn %q after provider binding", agent.activeTurnID, localTurnID)
	}

	ev := completedEventAt(
		"agent-1",
		"agent-1",
		providerTurnID,
		true,
		"",
		time.Unix(1781685693, 0).UTC(),
	)
	ev.Result = "B 已完成，父 agent 应该能读到这个 report"

	handleTurnCompletedEvent(svc, silentLogger(), ev)

	got, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if got.State != string(agentdto.StateIdle) {
		t.Fatalf("GetReport().State = %q, want %q", got.State, agentdto.StateIdle)
	}
	if got.Report != "B 已完成，父 agent 应该能读到这个 report" {
		t.Fatalf("GetReport().Report = %q, want completion result", got.Report)
	}
	if got.Metadata != nil && len(got.Metadata.RequesterIDs) != 0 {
		t.Fatalf("GetReport().Metadata.RequesterIDs = %#v, want drained", got.Metadata.RequesterIDs)
	}
}

func TestHandleTurnCompletedEventIgnoresStaleProviderTurnAfterReuse(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.activeTurnID = "local-turn-1"
	svc.registry.agents[agent.id] = agent
	if err := svc.BindActiveTurnID(context.Background(), agent.id, "provider-turn-1"); err != nil {
		t.Fatalf("BindActiveTurnID() error = %v", err)
	}

	// The first generation has settled; a new local claim must discard its provider alias.
	agent.activeTurnID = ""
	agent.state = agentdto.StateTurnQueued
	agent.cmd = &exec.Cmd{}
	agent.queue.Enqueue(TurnSubmission{AgentID: agent.id, ThreadID: agent.threadID})
	work := svc.claimTurnWork(context.Background())
	if len(work) != 1 {
		t.Fatalf("claimTurnWork() produced %d items, want 1", len(work))
	}
	currentTurnID := agent.activeTurnID
	if currentTurnID == "" || currentTurnID == "provider-turn-1" {
		t.Fatalf("activeTurnID = %q, want a new local turn", currentTurnID)
	}

	handleTurnCompletedEvent(svc, silentLogger(), completedEvent("agent-1", "thread-1", "provider-turn-1", true, ""))

	if agent.state != agentdto.StateTurnStarting {
		t.Fatalf("agent.state = %q, want %q after stale completion", agent.state, agentdto.StateTurnStarting)
	}
	if agent.activeTurnID != currentTurnID {
		t.Fatalf("activeTurnID = %q, want current turn %q after stale completion", agent.activeTurnID, currentTurnID)
	}
}

func TestRegisterTurnLifecycleHandlesTurnInterrupted(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })

	svc := NewService(silentLogger(), dispatcher, nil, nil, nil, nil)
	startTurnLifecycle(t, dispatcher, svc)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.activeTurnID = "turn-1"
	svc.registry.agents[agent.id] = agent

	interruptAt := time.Unix(1710000000, 0).UTC()
	event.Publish(dispatcher, interruptedEventAt("agent-1", "thread-1", "turn-1", "user_cancelled", interruptAt))

	snapshot := waitForAgentState(t, svc, agent.id, string(agentdto.StateIdle))
	if snapshot.activeTurnID != "" {
		t.Fatalf("activeTurnID = %q, want empty", snapshot.activeTurnID)
	}
	if snapshot.lastError != "user_cancelled" {
		t.Fatalf("lastError = %q, want user_cancelled", snapshot.lastError)
	}
	if !snapshot.updatedAt.Equal(interruptAt) {
		t.Fatalf("updatedAt = %s, want %s", snapshot.updatedAt, interruptAt)
	}
}

func TestApprovalLifecycleIgnoresQueuedEventsAfterStop(t *testing.T) {
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })

	svc := NewService(silentLogger(), dispatcher, nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.activeTurnID = "turn-1"
	svc.registry.agents[agent.id] = agent

	lc := &stubLifecycle{}
	RegisterApprovalLifecycle(lc, dispatcher, svc, silentLogger())
	if len(lc.hooks) != 1 {
		t.Fatalf("RegisterApprovalLifecycle() hooks = %d, want 1", len(lc.hooks))
	}
	hook := lc.hooks[0]
	if hook.OnStart == nil || hook.OnStop == nil {
		t.Fatal("RegisterApprovalLifecycle() must install both OnStart and OnStop")
	}
	if err := hook.OnStart(context.Background()); err != nil {
		t.Fatalf("OnStart() error = %v", err)
	}

	event.Publish(dispatcher, approvalRequestedEvent("agent-1", "turn-1"))
	if err := hook.OnStop(context.Background()); err != nil {
		t.Fatalf("OnStop() error = %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	snapshot := readAgentSnapshot(t, svc, agent.id)
	if snapshot.state != string(agentdto.StateTurnRunning) || snapshot.activeTurnID != "turn-1" {
		t.Fatalf("agent after stopped approval lifecycle = state:%q activeTurnID:%q, want unchanged running turn", snapshot.state, snapshot.activeTurnID)
	}
}

func TestHandleTurnInterruptedEventIsIdempotent(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })

	svc := NewService(silentLogger(), dispatcher, nil, nil, nil, nil)
	startTurnLifecycle(t, dispatcher, svc)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.activeTurnID = "turn-1"
	svc.registry.agents[agent.id] = agent

	firstInterruptAt := time.Unix(1710000000, 0).UTC()
	secondInterruptAt := firstInterruptAt.Add(time.Minute)
	event.Publish(dispatcher, interruptedEventAt("agent-1", "thread-1", "turn-1", "user_cancelled", firstInterruptAt))
	waitForAgentState(t, svc, agent.id, string(agentdto.StateIdle))
	event.Publish(dispatcher, interruptedEventAt("agent-1", "thread-1", "turn-1", "user_cancelled", secondInterruptAt))

	assertAgentUpdatedAtStays(t, svc, agent.id, firstInterruptAt)
	snapshot := readAgentSnapshot(t, svc, agent.id)
	if snapshot.activeTurnID != "" {
		t.Fatalf("activeTurnID = %q, want empty", snapshot.activeTurnID)
	}
	if snapshot.lastError != "user_cancelled" {
		t.Fatalf("lastError = %q, want user_cancelled", snapshot.lastError)
	}
	if !snapshot.updatedAt.Equal(firstInterruptAt) {
		t.Fatalf("updatedAt = %s, want %s", snapshot.updatedAt, firstInterruptAt)
	}
}

func TestRemoteTurnInterruptedAuthErrorStopsAgentAndWritesReport(t *testing.T) {
	t.Parallel()

	launcher := &recordingSettledStopLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := svc.newAgentLocked("agent-remote")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-auth"
	agent.remoteThreadID = "thread-auth"
	agent.activeTurnID = "turn-auth"
	svc.registry.agents[agent.id] = agent

	svc.handleRemoteTurnInterrupted(context.Background(), interruptedEventAt(
		"agent-remote",
		"thread-auth",
		"turn-auth",
		"Authentication failed. Please log in again.",
		time.Unix(1710000000, 0).UTC(),
	))

	if launcher.stopCalls != 1 {
		t.Fatalf("launcher.stopCalls = %d, want 1 for auth interruption cleanup", launcher.stopCalls)
	}
	got, err := svc.GetReport(context.Background(), "agent-remote")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if got.Report == "" || !strings.Contains(got.Report, "Authentication failed") {
		t.Fatalf("GetReport().Report = %q, want auth interruption report", got.Report)
	}
	if !agent.stopRequested || agent.state == agentdto.StateTurnRunning || agent.state == agentdto.StateTurnQueued || agent.state == agentdto.StateTurnStarting {
		t.Fatalf("agent state after auth interruption = state:%q stopRequested:%v, want stop requested and not running/pending", agent.state, agent.stopRequested)
	}
}

func TestRemoteTurnInterruptedClaudeAPIConnectionRefusedStopsAgent(t *testing.T) {
	t.Parallel()

	launcher := &recordingSettledStopLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := svc.newAgentLocked("agent-remote")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-claude"
	agent.remoteThreadID = "thread-claude"
	agent.activeTurnID = "turn-claude"
	agent.launchSeq = 1
	svc.registry.agents[agent.id] = agent

	svc.handleRemoteTurnInterrupted(context.Background(), interruptedEventAt(
		"agent-remote",
		"thread-claude",
		"turn-claude",
		"API Error: Unable to connect to API (ConnectionRefused)",
		time.Unix(1710000000, 0).UTC(),
	))

	if launcher.stopCalls != 1 {
		t.Fatalf("launcher.stopCalls = %d, want 1 for Claude API connection refusal cleanup", launcher.stopCalls)
	}
	got, err := svc.GetReport(context.Background(), "agent-remote")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if got.Report == "" || !strings.Contains(got.Report, "Unable to connect to API") {
		t.Fatalf("GetReport().Report = %q, want Claude API connection refusal report", got.Report)
	}
	if !agent.stopRequested || agent.state != agentdto.StateStopped || agent.activeTurnID != "" {
		t.Fatalf("agent after cleanup = state:%q stopRequested:%v activeTurnID:%q, want stopped with cleared active turn", agent.state, agent.stopRequested, agent.activeTurnID)
	}
}

func TestRemoteTurnInterruptedClaudeModelUnavailableStopsAgent(t *testing.T) {
	t.Parallel()

	launcher := &recordingSettledStopLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := svc.newAgentLocked("agent-remote")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-claude"
	agent.remoteThreadID = "thread-claude"
	agent.activeTurnID = "turn-claude"
	agent.launchSeq = 1
	svc.registry.agents[agent.id] = agent

	reason := "There's an issue with the selected model (gpt-5.5). It may not exist or you may not have access to it. Run --model to pick a different model."
	svc.handleRemoteTurnInterrupted(context.Background(), interruptedEventAt(
		"agent-remote",
		"thread-claude",
		"turn-claude",
		reason,
		time.Unix(1710000000, 0).UTC(),
	))

	if launcher.stopCalls != 1 {
		t.Fatalf("launcher.stopCalls = %d, want 1 for Claude model unavailable cleanup", launcher.stopCalls)
	}
	got, err := svc.GetReport(context.Background(), "agent-remote")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if got.Report == "" || !strings.Contains(got.Report, "selected model") {
		t.Fatalf("GetReport().Report = %q, want Claude model unavailable report", got.Report)
	}
	if !agent.stopRequested || agent.state != agentdto.StateStopped || agent.activeTurnID != "" {
		t.Fatalf("agent after cleanup = state:%q stopRequested:%v activeTurnID:%q, want stopped with cleared active turn", agent.state, agent.stopRequested, agent.activeTurnID)
	}
}

func TestRemoteTurnInterruptedClaudeModelUnavailableFinalStateVisibleAsStopped(t *testing.T) {
	t.Parallel()

	launcher := &recordingSettledStopLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := svc.newAgentLocked("agent-remote")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-claude"
	agent.remoteThreadID = "thread-claude"
	agent.activeTurnID = "turn-claude"
	agent.launchSeq = 1
	svc.registry.agents[agent.id] = agent

	reason := "There's an issue with the selected model (gpt-5.5). It may not exist or you may not have access to it. Run --model to pick a different model."
	svc.handleRemoteTurnInterrupted(context.Background(), interruptedEventAt(
		"agent-remote",
		"thread-claude",
		"turn-claude",
		reason,
		time.Unix(1710000000, 0).UTC(),
	))

	report, err := svc.GetReport(context.Background(), "agent-remote")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if report.State != string(agentdto.StateStopped) {
		t.Fatalf("GetReport().State = %q, want %q", report.State, agentdto.StateStopped)
	}
	if report.Report == "" || !strings.Contains(report.Report, "selected model") {
		t.Fatalf("GetReport().Report = %q, want Claude model unavailable report", report.Report)
	}

	assertOnlyListedAgentState(t, svc, "agent-remote", agentdto.StateStopped)
}

func TestRemoteTurnCompletedClaudeModelUnavailableFinalStateVisibleAsStopped(t *testing.T) {
	t.Parallel()

	launcher := &recordingSettledStopLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := svc.newAgentLocked("agent-remote")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-claude"
	agent.remoteThreadID = "thread-claude"
	agent.activeTurnID = "turn-claude"
	agent.launchSeq = 1
	svc.registry.agents[agent.id] = agent

	reason := "There's an issue with the selected model (gpt-5.5). It may not exist or you may not have access to it. Run --model to pick a different model."
	svc.handleRemoteTurnCompleted(context.Background(), completedEventAt(
		"agent-remote",
		"thread-claude",
		"turn-claude",
		false,
		reason,
		time.Unix(1710000000, 0).UTC(),
	))

	report, err := svc.GetReport(context.Background(), "agent-remote")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if report.State != string(agentdto.StateStopped) {
		t.Fatalf("GetReport().State = %q, want %q", report.State, agentdto.StateStopped)
	}
	if report.Report == "" || !strings.Contains(report.Report, "selected model") {
		t.Fatalf("GetReport().Report = %q, want Claude model unavailable report", report.Report)
	}

	assertOnlyListedAgentState(t, svc, "agent-remote", agentdto.StateStopped)
}

func TestHookTurnCompletedClaudeModelUnavailableFinalStateVisibleAsStopped(t *testing.T) {
	t.Parallel()

	launcher := &recordingSettledStopLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	consumer := newHookConsumer(svc, silentLogger())
	agent := svc.newAgentLocked("agent-remote")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-claude"
	agent.remoteThreadID = "thread-claude"
	agent.activeTurnID = "turn-claude"
	agent.launchSeq = 1
	svc.registry.agents[agent.id] = agent

	reason := "There's an issue with the selected model (gpt-5.5). It may not exist or you may not have access to it. Run --model to pick a different model."
	consumer.handleTurnCompleted(context.Background(), completedEventAt("agent-remote", "thread-claude", "turn-claude", false, reason, time.Unix(1710000000, 0).UTC()))

	report, err := svc.GetReport(context.Background(), "agent-remote")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if report.State != string(agentdto.StateStopped) {
		t.Fatalf("GetReport().State = %q, want %q", report.State, agentdto.StateStopped)
	}
	if report.Report == "" || !strings.Contains(report.Report, "selected model") {
		t.Fatalf("GetReport().Report = %q, want Claude model unavailable report", report.Report)
	}
}

func TestRemoteTurnInterruptedEmptyReasonWritesTerminalFallbackReport(t *testing.T) {
	t.Parallel()

	launcher := &recordingStallLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := svc.newAgentLocked("agent-remote")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-empty"
	agent.remoteThreadID = "thread-empty"
	agent.activeTurnID = "turn-empty"
	svc.registry.agents[agent.id] = agent

	svc.handleRemoteTurnInterrupted(context.Background(), interruptedEventAt(
		"agent-remote",
		"thread-empty",
		"turn-empty",
		"",
		time.Unix(1710000000, 0).UTC(),
	))

	if launcher.stopCalls != 0 {
		t.Fatalf("launcher.stopCalls = %d, want 0 for empty interruption reason", launcher.stopCalls)
	}
	got, err := svc.GetReport(context.Background(), "agent-remote")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if got.Report == "" || !strings.Contains(got.Report, "without producing a turn report") {
		t.Fatalf("GetReport().Report = %q, want terminal fallback report", got.Report)
	}
}

func TestHandleTurnCompletedEventConvergesAfterInterrupt(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })

	svc := NewService(silentLogger(), dispatcher, nil, nil, nil, nil)
	startTurnLifecycle(t, dispatcher, svc)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.activeTurnID = "turn-1"
	svc.registry.agents[agent.id] = agent

	interruptAt := time.Unix(1710000000, 0).UTC()
	completedAt := interruptAt.Add(time.Minute)
	event.Publish(dispatcher, interruptedEventAt("agent-1", "thread-1", "turn-1", "user_cancelled", interruptAt))
	waitForAgentState(t, svc, agent.id, string(agentdto.StateIdle))
	event.Publish(dispatcher, completedEventAt("agent-1", "thread-1", "turn-1", true, "", completedAt))

	assertAgentUpdatedAtStays(t, svc, agent.id, interruptAt)
	snapshot := readAgentSnapshot(t, svc, agent.id)
	if snapshot.activeTurnID != "" {
		t.Fatalf("activeTurnID = %q, want empty", snapshot.activeTurnID)
	}
	if snapshot.lastError != "user_cancelled" {
		t.Fatalf("lastError = %q, want user_cancelled", snapshot.lastError)
	}
	if !snapshot.updatedAt.Equal(interruptAt) {
		t.Fatalf("updatedAt = %s, want %s", snapshot.updatedAt, interruptAt)
	}
}

func TestLogTurnCompletionFailureDowngradesAgentNotFound(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	logTurnCompletionFailure(logger, completedEvent("agent-1", "thread-1", "turn-1", false, ""), errAgentNotFound, false, nil)

	output := buf.String()
	if !strings.Contains(output, "level=DEBUG") {
		t.Fatalf("output = %q, want DEBUG", output)
	}
	if strings.Contains(output, "level=WARN") {
		t.Fatalf("output = %q, want no WARN", output)
	}
}

func TestLogTurnCompletionFailureKeepsUnexpectedErrorsWarn(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	logTurnCompletionFailure(logger, completedEvent("agent-1", "thread-1", "turn-1", false, ""), errors.New("boom"), false, nil)

	if output := buf.String(); !strings.Contains(output, "level=WARN") {
		t.Fatalf("output = %q, want WARN", output)
	}
}

func completedEvent(agentID, threadID, turnID string, success bool, errMsg string) turndto.TurnCompleted {
	return completedEventAt(agentID, threadID, turnID, success, errMsg, time.Now())
}

func completedEventAt(agentID, threadID, turnID string, success bool, errMsg string, timestamp time.Time) turndto.TurnCompleted {
	return turndto.TurnCompleted{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{
					EventHeader: shared.EventHeader{Timestamp: timestamp},
					ThreadID:    threadID,
				},
				AgentID: agentID,
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: turnID},
		},
		Success: success,
		Error:   errMsg,
	}
}

func interruptedEventAt(agentID, threadID, turnID, reason string, timestamp time.Time) turndto.TurnInterrupted {
	return turndto.TurnInterrupted{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{
					EventHeader: shared.EventHeader{Timestamp: timestamp},
					ThreadID:    threadID,
				},
				AgentID: agentID,
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: turnID},
		},
		Reason: reason,
	}
}

func startTurnLifecycle(t *testing.T, dispatcher *event.Dispatcher, svc *service) {
	t.Helper()

	lc := &stubLifecycle{}
	RegisterTurnLifecycle(lc, dispatcher, svc, silentLogger())
	if len(lc.hooks) != 1 {
		t.Fatalf("RegisterTurnLifecycle() hooks = %d, want 1", len(lc.hooks))
	}
	hook := lc.hooks[0]
	if hook.OnStart == nil {
		t.Fatal("RegisterTurnLifecycle() OnStart = nil")
	}
	if err := hook.OnStart(context.Background()); err != nil {
		t.Fatalf("OnStart() error = %v", err)
	}
	t.Cleanup(func() {
		if hook.OnStop != nil {
			if err := hook.OnStop(context.Background()); err != nil {
				t.Errorf("OnStop() error = %v", err)
			}
		}
	})
}

func waitForAgentState(t *testing.T, svc *service, agentID, wantState string) agentSnapshot {
	t.Helper()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		snapshot := readAgentSnapshot(t, svc, agentID)
		if snapshot.state == wantState {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	snapshot := readAgentSnapshot(t, svc, agentID)
	t.Fatalf("agent.state = %q, want %q", snapshot.state, wantState)
	return agentSnapshot{}
}

func assertAgentUpdatedAtStays(t *testing.T, svc *service, agentID string, want time.Time) {
	t.Helper()

	deadline := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(deadline) {
		snapshot := readAgentSnapshot(t, svc, agentID)
		if !snapshot.updatedAt.Equal(want) {
			t.Fatalf("updatedAt = %s, want %s", snapshot.updatedAt, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func readAgentSnapshot(t *testing.T, svc *service, agentID string) agentSnapshot {
	t.Helper()

	var snapshot agentSnapshot
	if err := svc.registry.withAgentReadLocked(agentID, func(agent *agentRuntime) error {
		snapshot = agentSnapshot{
			state:        string(agent.state),
			activeTurnID: agent.activeTurnID,
			lastError:    agent.lastError,
			updatedAt:    agent.updatedAt,
		}
		return nil
	}); err != nil {
		t.Fatalf("registry.withAgentReadLocked(%q) error = %v", agentID, err)
	}
	return snapshot
}

func assertOnlyListedAgentState(t *testing.T, svc *service, agentID string, want agentdto.AgentState) {
	t.Helper()

	snapshots, err := svc.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].ID != agentID {
		t.Fatalf("ListAgents() = %#v, want only %s", snapshots, agentID)
	}
	if snapshots[0].State != string(want) {
		t.Fatalf("ListAgents()[%s].State = %q, want %q", agentID, snapshots[0].State, want)
	}
}
