package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/kelindar/event"
)

func TestRecoverReplaysStoreBackedActiveTurnAheadOfQueuedWork(t *testing.T) {
	t.Parallel()

	svc, agent := newRecoverReplayService(t)
	if err := svc.Recover(context.Background(), agent.id); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	assertRecoveredReplayQueue(t, agent)
}

func TestRecoveredReplayCurrentThreadStoppedWritesFallback(t *testing.T) {
	t.Parallel()

	svc, agent := newRecoverReplayService(t)
	agent.reportRequesters = []string{"agent-parent"}
	if err := svc.Recover(context.Background(), agent.id); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	work := svc.claimTurnWork(context.Background())
	if len(work) != 1 || work[0].threadID != "thread-1" {
		t.Fatalf("claimTurnWork() = %#v, want recovered work on thread-1", work)
	}

	newHookConsumer(svc, silentLogger()).handleThreadStopped(context.Background(), threaddto.Stopped{
		EventHeader: shareddto.EventHeader{Timestamp: time.Now().Add(time.Hour)},
		ThreadID:    "thread-1",
		AgentID:     "agent-1",
		Reason:      "process_exited",
	})
	got, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if !strings.Contains(got.Report, "without producing a turn report") {
		t.Fatalf("GetReport().Report = %q, want stopped fallback report", got.Report)
	}
	if len(agent.reportRequesters) != 0 {
		t.Fatalf("agent.reportRequesters = %v, want drained", agent.reportRequesters)
	}
}

func newRecoverReplayService(t *testing.T) (*service, *agentRuntime) {
	t.Helper()
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.lifecycle.recoveryStore = stubRecoveryTurnStore{
		nodes: []taskdag.Node{{
			DagKey:         "dag-1",
			NodeKey:        "node-1",
			AssignedTo:     "agent-1",
			ActiveTurnID:   testStringPtr("turn-active"),
			ActiveWakeupID: testInt64Ptr(7),
		}},
		wakeups: map[int64]taskdag.Wakeup{
			7: {
				ID:            7,
				Status:        "sent",
				TargetAgentID: "agent-1",
				BoundTurnID:   testStringPtr("turn-active"),
				TurnBoundAt:   testTimePtr(t),
				PromptPayload: json.RawMessage(`{"agentId":"agent-1","prompt":"replay me","selectedSkills":["debug"]}`),
			},
		},
	}

	agent := svc.newAgentLocked("agent-1")
	agent.command = longRunningTestCommandLine()
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.activeTurnID = "turn-active"
	agent.queue.Enqueue(TurnSubmission{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
		Inputs:   []shareddto.InputItem{{Type: "text", Content: "queued work"}},
	})
	svc.registry.agents[agent.id] = agent
	t.Cleanup(func() { cleanupAgentProcess(agent) })
	return svc, agent
}

func assertRecoveredReplayQueue(t *testing.T, agent *agentRuntime) {
	t.Helper()
	if agent.state != agentdto.StateTurnQueued {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateTurnQueued)
	}
	if got := agent.queue.Len(); got != 2 {
		t.Fatalf("queue.Len() = %d, want 2", got)
	}
	assertFirstRecoveredReplayTurn(t, dequeueRecoveredTurn(t, agent, "first"))
	assertSecondRecoveredReplayTurn(t, dequeueRecoveredTurn(t, agent, "second"))
}

func dequeueRecoveredTurn(t *testing.T, agent *agentRuntime, label string) TurnSubmission {
	t.Helper()
	turn, ok := agent.queue.Dequeue()
	if !ok {
		t.Fatalf("%s Dequeue() ok = false, want true", label)
	}
	return turn
}

func assertFirstRecoveredReplayTurn(t *testing.T, first TurnSubmission) {
	t.Helper()
	if first.ExpectedTurnID != "turn-active" {
		t.Fatalf("first.ExpectedTurnID = %q, want turn-active", first.ExpectedTurnID)
	}
	if first.ThreadID != "thread-1" {
		t.Fatalf("first.ThreadID = %q, want thread-1", first.ThreadID)
	}
	if len(first.Inputs) != 1 || first.Inputs[0].Content != "replay me" {
		t.Fatalf("first.Inputs = %#v, want replayed prompt", first.Inputs)
	}
}

func assertSecondRecoveredReplayTurn(t *testing.T, second TurnSubmission) {
	t.Helper()
	if len(second.Inputs) != 1 || second.Inputs[0].Content != "queued work" {
		t.Fatalf("second.Inputs = %#v, want queued work", second.Inputs)
	}
}

func cleanupAgentProcess(agent *agentRuntime) {
	if agent.cmd != nil {
		_ = stopProcess(agent.cmd)
	}
}

func TestRecoverPublishesTurnResumedForRecoveredTurn(t *testing.T) {
	t.Parallel()
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	svc := NewService(silentLogger(), dispatcher, nil, nil, nil, nil)
	svc.lifecycle.recoveryStore = replayableRecoveryTurnStore()
	resumedEvents := make(chan turndto.TurnResumed, 1)
	cancel := event.Subscribe(dispatcher, func(ev turndto.TurnResumed) {
		resumedEvents <- ev
	})
	t.Cleanup(cancel)
	agent := svc.newAgentLocked("agent-1")
	agent.command = longRunningTestCommandLine()
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.activeTurnID = "turn-active"
	agent.updatedAt = time.Unix(1710000000, 0).UTC()
	svc.registry.agents[agent.id] = agent
	t.Cleanup(func() {
		if agent.cmd != nil {
			_ = stopProcess(agent.cmd)
		}
	})
	if err := svc.Recover(context.Background(), agent.id); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	ev := awaitTurnResumed(t, resumedEvents)
	if ev.AgentID != "agent-1" {
		t.Fatalf("TurnResumed.AgentID = %q, want agent-1", ev.AgentID)
	}
	if ev.ThreadID != "thread-1" {
		t.Fatalf("TurnResumed.ThreadID = %q, want thread-1", ev.ThreadID)
	}
	if ev.TurnID != "turn-active" {
		t.Fatalf("TurnResumed.TurnID = %q, want turn-active", ev.TurnID)
	}
	if ev.Reason != turnResumeReasonRecover {
		t.Fatalf("TurnResumed.Reason = %q, want %q", ev.Reason, turnResumeReasonRecover)
	}
}

func TestRecoverWithoutActiveTurnDoesNotPublishTurnResumed(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	svc := NewService(silentLogger(), dispatcher, nil, nil, nil, nil)
	resumedEvents := make(chan turndto.TurnResumed, 1)
	cancel := event.Subscribe(dispatcher, func(ev turndto.TurnResumed) { resumedEvents <- ev })
	t.Cleanup(cancel)
	agent := svc.newAgentLocked("agent-1")
	agent.command = longRunningTestCommandLine()
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	svc.registry.agents[agent.id] = agent
	t.Cleanup(func() {
		if agent.cmd != nil {
			_ = stopProcess(agent.cmd)
		}
	})

	if err := svc.Recover(context.Background(), agent.id); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	select {
	case ev := <-resumedEvents:
		t.Fatalf("unexpected TurnResumed event: %+v", ev)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestRecoverWithoutReplayWritesFallbackReport(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.command = longRunningTestCommandLine()
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.activeTurnID = "turn-active"
	agent.reportRequesters = []string{"agent-parent"}
	agent.lastError = "process crashed"
	svc.registry.agents[agent.id] = agent
	t.Cleanup(func() { cleanupAgentProcess(agent) })

	if err := svc.Recover(context.Background(), agent.id); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if !strings.Contains(agent.lastReport, "process crashed") || !strings.Contains(agent.lastReport, "without producing a turn report") {
		t.Fatalf("agent.lastReport = %q, want no-replay fallback with crash detail", agent.lastReport)
	}
	if len(agent.reportRequesters) != 0 {
		t.Fatalf("agent.reportRequesters = %v, want drained", agent.reportRequesters)
	}
}

func TestRecoverStalledAgentsPublishesTurnStalledAndResumed(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })

	svc := NewService(silentLogger(), dispatcher, nil, nil, nil, nil)
	svc.lifecycle.recoveryStore = replayableRecoveryTurnStore()
	stalledEvents := make(chan turndto.TurnStalled, 1)
	resumedEvents := make(chan turndto.TurnResumed, 1)
	stalledCancel := event.Subscribe(dispatcher, func(ev turndto.TurnStalled) {
		stalledEvents <- ev
	})
	resumedCancel := event.Subscribe(dispatcher, func(ev turndto.TurnResumed) {
		resumedEvents <- ev
	})
	t.Cleanup(stalledCancel)
	t.Cleanup(resumedCancel)

	agent := svc.newAgentLocked("agent-1")
	agent.command = longRunningTestCommandLine()
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.activeTurnID = "turn-active"
	agent.updatedAt = time.Now().Add(-time.Minute)
	svc.registry.agents[agent.id] = agent
	t.Cleanup(func() { cleanupAgentProcess(agent) })

	actor := &runnerActor{logger: silentLogger(), lifecycle: svc.lifecycle, runtime: svc}
	actor.recoverStalledAgents(context.Background(), &StallDetector{
		threshold: 30 * time.Second,
		logger:    silentLogger(),
	})

	stalled := awaitTurnStalled(t, stalledEvents)
	assertRecoverStalledEvent(t, stalled)

	resumed := awaitTurnResumed(t, resumedEvents)
	assertRecoverResumedEvent(t, resumed)
}

func TestRecoverStalledAgentsRestartsLauncherOwnedAgent(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })

	launcher := &recordingStallLauncher{}
	svc := NewService(silentLogger(), dispatcher, launcher, nil, nil, nil)
	stalledEvents := make(chan turndto.TurnStalled, 2)
	resumedEvents := make(chan turndto.TurnResumed, 1)
	stalledCancel := event.Subscribe(dispatcher, func(ev turndto.TurnStalled) {
		stalledEvents <- ev
	})
	resumedCancel := event.Subscribe(dispatcher, func(ev turndto.TurnResumed) {
		resumedEvents <- ev
	})
	t.Cleanup(stalledCancel)
	t.Cleanup(resumedCancel)

	oldUpdatedAt := time.Now().Add(-time.Minute)
	agent := svc.newAgentLocked("agent-remote")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-remote"
	agent.remoteThreadID = "thread-remote"
	agent.activeTurnID = "turn-remote"
	agent.updatedAt = oldUpdatedAt
	svc.registry.agents[agent.id] = agent

	actor := &runnerActor{logger: silentLogger(), lifecycle: svc.lifecycle, runtime: svc}
	detector := &StallDetector{threshold: 30 * time.Second, logger: silentLogger()}
	actor.recoverStalledAgents(context.Background(), detector)

	stalled := awaitTurnStalled(t, stalledEvents)
	if stalled.AgentID != "agent-remote" || stalled.ThreadID != "thread-remote" || stalled.TurnID != "turn-remote" {
		t.Fatalf("TurnStalled = %+v, want remote agent/thread/turn", stalled)
	}
	assertLauncherOwnedStallRecovery(t, launcher, agent, resumedEvents)
}

func assertLauncherOwnedStallRecovery(t *testing.T, launcher *recordingStallLauncher, agent *agentRuntime, resumedEvents <-chan turndto.TurnResumed) {
	t.Helper()
	if launcher.launchCalls != 1 || launcher.stopCalls != 1 {
		t.Fatalf("launcher calls = launch:%d stop:%d, want stop once and launch once", launcher.launchCalls, launcher.stopCalls)
	}
	if agent.state != agentdto.StateIdle || agent.activeTurnID != "" || agent.remoteThreadID == "" {
		t.Fatalf("agent state after stalled recovery = state:%q active:%q remote:%q, want idle recovered remote runtime", agent.state, agent.activeTurnID, agent.remoteThreadID)
	}
	select {
	case ev := <-resumedEvents:
		t.Fatalf("unexpected TurnResumed event for launcher-owned agent: %+v", ev)
	default:
	}
}

func TestHandleProcessExitErrorAutoRecoversLocalAgent(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-local")
	agent.command = longRunningTestCommandLine()
	agent.state = agentdto.StateIdle
	agent.launchSeq = 1
	svc.registry.agents[agent.id] = agent
	t.Cleanup(func() { cleanupAgentProcess(agent) })

	svc.handleProcessExit(context.Background(), agent.id, 1, errors.New("process crashed"))

	if agent.state != agentdto.StateIdle || agent.cmd == nil || agent.launchSeq <= 1 {
		t.Fatalf("agent after process exit recovery = state:%q cmd:%v launchSeq:%d, want relaunched idle agent", agent.state, agent.cmd != nil, agent.launchSeq)
	}
}

func TestProcessExitAutoRecoveryStopsAtRetryLimit(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-local")
	agent.command = longRunningTestCommandLine()

	for i := range maxProcessExitAutoRecoveries {
		if !shouldAutoRecoverProcessExitLocked(svc, agent, errors.New("process crashed")) {
			t.Fatalf("shouldAutoRecoverProcessExitLocked() attempt %d = false, want true before limit", i+1)
		}
	}
	if shouldAutoRecoverProcessExitLocked(svc, agent, errors.New("process crashed")) {
		t.Fatalf("shouldAutoRecoverProcessExitLocked() after retry limit = true, want false")
	}
	if !strings.Contains(agent.lastError, "auto recovery retry limit reached") {
		t.Fatalf("agent.lastError = %q, want retry limit detail", agent.lastError)
	}
}

func TestStallDetectorTreatsLocalProcessWithRemoteThreadIDAsRecoverable(t *testing.T) {
	t.Parallel()

	detector := &StallDetector{threshold: 30 * time.Second, logger: silentLogger()}
	agent := &agentRuntime{
		state:          agentdto.StateTurnRunning,
		cmd:            &exec.Cmd{},
		remoteThreadID: "thread-local",
		updatedAt:      time.Now().Add(-time.Minute),
	}
	if !detector.CheckStall(agent) {
		t.Fatal("CheckStall() = false, want true for local process with reported remote thread id")
	}
}

func assertRecoverStalledEvent(t *testing.T, stalled turndto.TurnStalled) {
	t.Helper()
	if stalled.AgentID != "agent-1" {
		t.Fatalf("TurnStalled.AgentID = %q, want agent-1", stalled.AgentID)
	}
	if stalled.ThreadID != "thread-1" {
		t.Fatalf("TurnStalled.ThreadID = %q, want thread-1", stalled.ThreadID)
	}
	if stalled.TurnID != "turn-active" {
		t.Fatalf("TurnStalled.TurnID = %q, want turn-active", stalled.TurnID)
	}
	if stalled.Reason != recoverReasonStall {
		t.Fatalf("TurnStalled.Reason = %q, want %q", stalled.Reason, recoverReasonStall)
	}
	if stalled.StalledMS < 30_000 {
		t.Fatalf("TurnStalled.StalledMS = %d, want >= 30000", stalled.StalledMS)
	}
}

func assertRecoverResumedEvent(t *testing.T, resumed turndto.TurnResumed) {
	t.Helper()
	if resumed.AgentID != "agent-1" {
		t.Fatalf("TurnResumed.AgentID = %q, want agent-1", resumed.AgentID)
	}
	if resumed.ThreadID != "thread-1" {
		t.Fatalf("TurnResumed.ThreadID = %q, want thread-1", resumed.ThreadID)
	}
	if resumed.TurnID != "turn-active" {
		t.Fatalf("TurnResumed.TurnID = %q, want turn-active", resumed.TurnID)
	}
	if resumed.Reason != turnResumeReasonRecover {
		t.Fatalf("TurnResumed.Reason = %q, want %q", resumed.Reason, turnResumeReasonRecover)
	}
}

func TestLoadRecoveredTurnSubmissionSkipsReclaimedWakeup(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.lifecycle.recoveryStore = stubRecoveryTurnStore{
		nodes: []taskdag.Node{{
			DagKey:         "dag-1",
			NodeKey:        "node-1",
			AssignedTo:     "agent-1",
			ActiveTurnID:   testStringPtr("turn-active"),
			ActiveWakeupID: testInt64Ptr(7),
		}},
		wakeups: map[int64]taskdag.Wakeup{
			7: {
				ID:            7,
				Status:        "pending",
				TargetAgentID: "agent-1",
				PromptPayload: json.RawMessage(`{"agentId":"agent-1","prompt":"stale"}`),
			},
		},
	}
	agent := &agentRuntime{id: "agent-1", threadID: "thread-1", activeTurnID: "turn-active"}

	submission, shouldReplay, err := loadRecoveredTurnSubmission(context.Background(), svc, agent)
	if err != nil {
		t.Fatalf("loadRecoveredTurnSubmission() error = %v", err)
	}
	if shouldReplay {
		t.Fatalf("shouldReplay = true, want false with reclaimed wakeup: %#v", submission)
	}
	if got := len(submission.Inputs); got != 0 {
		t.Fatalf("submission.Inputs len = %d, want 0", got)
	}
}

func TestFireOrForceLockedIncludesContextForIllegalTransition(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle

	err := svc.fireOrForceLocked(context.Background(), agent, agentdto.TriggerTurnAccepted)
	if err == nil {
		t.Fatal("fireOrForceLocked() error = nil, want non-nil")
	}
	for _, want := range []string{
		`state=idle`,
		`trigger=turn_accepted`,
		`turn_enqueued`,
		`recover_requested`,
		`stop_requested`,
		`process_exited`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("fireOrForceLocked() error = %q, want substring %q", err, want)
		}
	}
}

func TestFireOrForceLockedIncludesContextWhenStateMachineMissing(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	agent := &agentRuntime{id: "agent-1", state: agentdto.StateIdle}

	err := svc.fireOrForceLocked(context.Background(), agent, agentdto.TriggerTurnAccepted)
	if err == nil {
		t.Fatal("fireOrForceLocked() error = nil, want non-nil")
	}
	for _, want := range []string{
		`state=idle`,
		`trigger=turn_accepted`,
		`turn_enqueued`,
		`state machine is not initialized`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("fireOrForceLocked() error = %q, want substring %q", err, want)
		}
	}
}

type stubRecoveryTurnStore struct {
	nodes   []taskdag.Node
	wakeups map[int64]taskdag.Wakeup
	listErr error
	getErr  error
}

func replayableRecoveryTurnStore() stubRecoveryTurnStore {
	return stubRecoveryTurnStore{
		nodes: []taskdag.Node{{
			DagKey:         "dag-1",
			NodeKey:        "node-1",
			AssignedTo:     "agent-1",
			ActiveTurnID:   testStringPtr("turn-active"),
			ActiveWakeupID: testInt64Ptr(7),
		}},
		wakeups: map[int64]taskdag.Wakeup{
			7: {
				ID:            7,
				Status:        "sent",
				TargetAgentID: "agent-1",
				BoundTurnID:   testStringPtr("turn-active"),
				TurnBoundAt:   testTimePtrValue(),
				PromptPayload: json.RawMessage(`{"agentId":"agent-1","prompt":"replay me"}`),
			},
		},
	}
}

func (s stubRecoveryTurnStore) ListRunningNodesByAssignee(_ context.Context, assignee string) ([]taskdag.Node, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	filtered := make([]taskdag.Node, 0, len(s.nodes))
	for _, node := range s.nodes {
		if node.AssignedTo == assignee {
			filtered = append(filtered, node)
		}
	}
	return filtered, nil
}

func (s stubRecoveryTurnStore) GetWakeup(_ context.Context, id int64) (*taskdag.Wakeup, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	wakeup, ok := s.wakeups[id]
	if !ok {
		return nil, errors.New("wakeup not found")
	}
	return &wakeup, nil
}

func testStringPtr(value string) *string { return &value }

func testInt64Ptr(value int64) *int64 { return &value }

func testTimePtr(t *testing.T) *time.Time {
	t.Helper()
	return testTimePtrValue()
}

func testTimePtrValue() *time.Time {
	now := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)
	return &now
}

func awaitTurnStalled(t *testing.T, events <-chan turndto.TurnStalled) turndto.TurnStalled {
	t.Helper()

	select {
	case ev := <-events:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for TurnStalled event")
		var zero turndto.TurnStalled
		return zero
	}
}

func awaitTurnResumed(t *testing.T, events <-chan turndto.TurnResumed) turndto.TurnResumed {
	t.Helper()

	select {
	case ev := <-events:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for TurnResumed event")
		var zero turndto.TurnResumed
		return zero
	}
}

type recordingStallLauncher struct {
	launchCalls    int
	stopCalls      int
	launchAgent    *agentRuntime
	stopAgent      *agentRuntime
	launchReq      LaunchRequest
	launchErr      error
	stopErr        error
	submitErr      error
	submitCalls    int
	submitThreadID string
	remoteAgentID  string
	stopThreads    []string
	afterLaunch    func()
}

func (l *recordingStallLauncher) Launch(_ context.Context, agent *agentRuntime, req LaunchRequest) (LaunchResult, error) {
	l.launchCalls++
	l.launchAgent = agent
	l.launchReq = req
	if l.launchErr != nil {
		return LaunchResult{}, l.launchErr
	}
	remoteAgentID := strings.TrimSpace(l.remoteAgentID)
	if remoteAgentID == "" {
		remoteAgentID = agent.id
	}
	agent.remoteThreadID = "thread-recovered"
	agent.remoteAgentID = remoteAgentID
	agent.launchSeq++
	if l.afterLaunch != nil {
		l.afterLaunch()
	}
	return LaunchResult{ThreadID: agent.remoteThreadID, RemoteAgentID: remoteAgentID}, nil
}

func (l *recordingStallLauncher) Fork(context.Context, *agentRuntime, *agentRuntime, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, errors.New("fork should not be called")
}

func (l *recordingStallLauncher) Stop(_ context.Context, agent *agentRuntime) error {
	l.stopCalls++
	l.stopAgent = agent
	if agent != nil {
		l.stopThreads = append(l.stopThreads, agent.remoteThreadID)
	}
	if l.stopErr != nil {
		return l.stopErr
	}
	return nil
}

func (l *recordingStallLauncher) Archive(ctx context.Context, agent *agentRuntime) error {
	return l.Stop(ctx, agent)
}

func (l *recordingStallLauncher) Interrupt(context.Context, *agentRuntime, string) error {
	return nil
}

func (l *recordingStallLauncher) SubmitTurn(_ context.Context, agent *agentRuntime, _ TurnSubmission) (string, error) {
	l.submitCalls++
	if l.submitErr != nil {
		return "", l.submitErr
	}
	if agent != nil {
		l.submitThreadID = agent.remoteThreadID
	}
	return "turn-remote", nil
}

func (l *recordingStallLauncher) IsRunning(_ context.Context, agent *agentRuntime) bool {
	return agent != nil && strings.TrimSpace(agent.remoteThreadID) != ""
}
