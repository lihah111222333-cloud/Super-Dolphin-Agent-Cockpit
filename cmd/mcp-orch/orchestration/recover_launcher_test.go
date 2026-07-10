package orchestration

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/kelindar/event"
)

func TestLauncherRecoveryUsesRuntimeCopyAndPreservesLaunchRequest(t *testing.T) {
	t.Parallel()

	launcher := &recordingStallLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	agent.prompt = "original prompt"
	agent.instructions = "base instructions"
	agent.agentType = "reviewer"
	agent.agentKey = "review-agent"
	agent.memoryScope = "project"
	agent.language = "zh"

	if err := svc.lifecycle.recovery.recoverWithReason(context.Background(), agent.id, recoverReasonStall); err != nil {
		t.Fatalf("recoverWithReason() error = %v", err)
	}
	if launcher.stopAgent == agent || launcher.launchAgent == agent {
		t.Fatalf("launcher received live runtime pointer; stop=%p launch=%p live=%p", launcher.stopAgent, launcher.launchAgent, agent)
	}
	if launcher.launchReq.Prompt != "original prompt" || launcher.launchReq.Instructions != "base instructions" ||
		launcher.launchReq.AgentType != "reviewer" || launcher.launchReq.AgentKey != "review-agent" ||
		launcher.launchReq.MemoryScope != "project" || launcher.launchReq.Language != "zh" {
		t.Fatalf("recovery launch request lost fields: %#v", launcher.launchReq)
	}
}

func TestLauncherRecoveryRekeysAndReplaysActiveTurn(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	launcher := &recordingStallLauncher{remoteAgentID: "agent-remote-new"}
	svc := NewService(silentLogger(), dispatcher, launcher, nil, nil, nil)
	svc.lifecycle.recoveryStore = launcherReplayStore(t, "agent-remote")
	resumedEvents := make(chan turndto.TurnResumed, 1)
	cancel := event.Subscribe(dispatcher, func(ev turndto.TurnResumed) { resumedEvents <- ev })
	t.Cleanup(cancel)
	agent := launcherRecoveryAgent(svc, "agent-remote")

	if err := svc.lifecycle.recovery.recoverWithReason(context.Background(), agent.id, recoverReasonStall); err != nil {
		t.Fatalf("recoverWithReason() error = %v", err)
	}
	if _, err := svc.Snapshot(context.Background(), "agent-remote"); !errors.Is(err, errAgentNotFound) {
		t.Fatalf("Snapshot(old id) error = %v, want agent not found after rekey", err)
	}
	snapshot, err := svc.Snapshot(context.Background(), "agent-remote-new")
	if err != nil {
		t.Fatalf("Snapshot(new id) error = %v", err)
	}
	if snapshot.State != string(agentdto.StateTurnQueued) || snapshot.ThreadID != "thread-recovered" {
		t.Fatalf("Snapshot(new id) = %#v, want queued on recovered thread", snapshot)
	}
	resumed := awaitTurnResumed(t, resumedEvents)
	if resumed.AgentID != "agent-remote-new" || resumed.ThreadID != "thread-remote" || resumed.TurnID != "turn-active" {
		t.Fatalf("TurnResumed = %+v, want rekeyed agent with original turn context", resumed)
	}
}

func TestLauncherProcessExitRecoveryPreservesRetryCounter(t *testing.T) {
	t.Parallel()

	launcher := &recordingStallLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	agent.autoRecoverSince = time.Now()
	agent.autoRecoverCount = 2

	if err := svc.lifecycle.recovery.recoverWithReason(context.Background(), agent.id, recoverReasonProcessExit); err != nil {
		t.Fatalf("recoverWithReason() error = %v", err)
	}
	if agent.autoRecoverCount != 2 || agent.autoRecoverSince.IsZero() {
		t.Fatalf("auto recovery counter after launcher recovery = count:%d since:%v, want preserved", agent.autoRecoverCount, agent.autoRecoverSince)
	}
}

func TestHandleProcessExitRecoversLauncherOwnedAgent(t *testing.T) {
	t.Parallel()

	launcher := &recordingStallLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	agent.launchSeq = 1

	svc.handleProcessExit(context.Background(), agent.id, 1, errors.New("remote process crashed"))

	if launcher.stopCalls != 1 || launcher.launchCalls != 1 {
		t.Fatalf("launcher calls after process exit = stop:%d launch:%d, want one recovery cycle", launcher.stopCalls, launcher.launchCalls)
	}
	if agent.state != agentdto.StateIdle || agent.remoteThreadID != "thread-recovered" {
		t.Fatalf("agent after launcher process-exit recovery = state:%q thread:%q, want recovered launcher runtime", agent.state, agent.remoteThreadID)
	}
}

func TestLauncherRecoveryStopsSuccessfulStaleLaunch(t *testing.T) {
	t.Parallel()

	launcher := &recordingStallLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	launcher.afterLaunch = func() {
		svc.registry.mu.Lock()
		agent.launchSeq++
		svc.registry.mu.Unlock()
	}

	err := svc.lifecycle.recovery.recoverWithReason(context.Background(), agent.id, recoverReasonStall)
	if !errors.Is(err, errAgentNotFound) {
		t.Fatalf("recoverWithReason() error = %v, want stale seq error", err)
	}
	if launcher.stopCalls != 2 || !containsString(launcher.stopThreads, "thread-recovered") {
		t.Fatalf("launcher stop calls=%d stopThreads=%v, want old stop plus stale launched thread cleanup", launcher.stopCalls, launcher.stopThreads)
	}
}

func TestLauncherRecoveryReturnsErrorWhenSuccessfulLaunchStateChanged(t *testing.T) {
	t.Parallel()

	launcher := &recordingStallLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	launcher.afterLaunch = func() {
		svc.registry.mu.Lock()
		agent.state = agentdto.StateIdle
		svc.registry.mu.Unlock()
	}

	err := svc.lifecycle.recovery.recoverWithReason(context.Background(), agent.id, recoverReasonStall)
	if err == nil || !strings.Contains(err.Error(), "state changed") {
		t.Fatalf("recoverWithReason() error = %v, want state changed stale recovery error", err)
	}
	if launcher.stopCalls != 2 || !containsString(launcher.stopThreads, "thread-recovered") {
		t.Fatalf("launcher stop calls=%d stopThreads=%v, want old stop plus stale launched thread cleanup", launcher.stopCalls, launcher.stopThreads)
	}
}

func TestLauncherRecoveryReturnsErrorWhenSuccessfulLaunchStopRequested(t *testing.T) {
	t.Parallel()

	launcher := &recordingStallLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	launcher.afterLaunch = func() {
		svc.registry.mu.Lock()
		agent.stopRequested = true
		svc.registry.mu.Unlock()
	}

	err := svc.lifecycle.recovery.recoverWithReason(context.Background(), agent.id, recoverReasonStall)
	if err == nil || !strings.Contains(err.Error(), "stop requested") {
		t.Fatalf("recoverWithReason() error = %v, want stop requested stale recovery error", err)
	}
	if launcher.stopCalls != 2 || !containsString(launcher.stopThreads, "thread-recovered") {
		t.Fatalf("launcher stop calls=%d stopThreads=%v, want old stop plus stale launched thread cleanup", launcher.stopCalls, launcher.stopThreads)
	}
}

func TestLauncherRecoveryReplayLoadFailureWritesFallback(t *testing.T) {
	t.Parallel()

	launcher := &recordingStallLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	svc.lifecycle.recoveryStore = stubRecoveryTurnStore{listErr: errors.New("recovery store unavailable")}
	agent := launcherRecoveryAgent(svc, "agent-remote")
	agent.reportRequesters = []string{"agent-parent"}

	err := svc.lifecycle.recovery.recoverWithReason(context.Background(), agent.id, recoverReasonStall)
	if err == nil || !strings.Contains(err.Error(), "recovery store unavailable") {
		t.Fatalf("recoverWithReason() error = %v, want recovery store error", err)
	}
	if launcher.stopCalls != 0 || launcher.launchCalls != 0 {
		t.Fatalf("launcher calls after replay load failure = stop:%d launch:%d, want none", launcher.stopCalls, launcher.launchCalls)
	}
	assertFailedRecoveryFallback(t, agent, "recovery store unavailable")
}

func TestLauncherRecoveryStopFailureWritesFallback(t *testing.T) {
	t.Parallel()

	launcher := &recordingStallLauncher{stopErr: errors.New("remote stop failed")}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	agent.reportRequesters = []string{"agent-parent"}

	err := svc.lifecycle.recovery.recoverWithReason(context.Background(), agent.id, recoverReasonStall)
	if err == nil || !strings.Contains(err.Error(), "remote stop failed") {
		t.Fatalf("recoverWithReason() error = %v, want stop error", err)
	}
	if launcher.stopCalls != 1 || launcher.launchCalls != 0 {
		t.Fatalf("launcher calls after stop failure = stop:%d launch:%d, want one stop and no launch", launcher.stopCalls, launcher.launchCalls)
	}
	assertFailedRecoveryFallback(t, agent, "remote stop failed")
}

func TestRecoverStalledAgentsFailureWritesFallback(t *testing.T) {
	t.Parallel()

	launcher := &recordingStallLauncher{launchErr: errors.New("remote launch failed")}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	agent.updatedAt = time.Now().Add(-time.Minute)
	agent.reportRequesters = []string{"agent-parent"}
	actor := &runnerActor{logger: silentLogger(), lifecycle: svc.lifecycle, runtime: svc}

	actor.recoverStalledAgents(context.Background(), &StallDetector{
		threshold: 30 * time.Second,
		logger:    silentLogger(),
	})

	if launcher.stopCalls != 1 || launcher.launchCalls != 1 {
		t.Fatalf("launcher calls after stalled failure = stop:%d launch:%d, want recovery attempt", launcher.stopCalls, launcher.launchCalls)
	}
	assertFailedRecoveryFallback(t, agent, "remote launch failed")
}

func TestLauncherRecoveryWithoutReplayWritesFallbackReport(t *testing.T) {
	t.Parallel()

	launcher := &recordingStallLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	agent.reportRequesters = []string{"agent-parent"}
	agent.lastError = "remote process crashed"

	if err := svc.lifecycle.recovery.recoverWithReason(context.Background(), agent.id, recoverReasonProcessExit); err != nil {
		t.Fatalf("recoverWithReason() error = %v", err)
	}
	if agent.state != agentdto.StateIdle || agent.activeTurnID != "" {
		t.Fatalf("agent after no-replay launcher recovery = state:%q active:%q, want idle with no active turn", agent.state, agent.activeTurnID)
	}
	if !strings.Contains(agent.lastReport, "remote process crashed") || !strings.Contains(agent.lastReport, "without producing a turn report") {
		t.Fatalf("agent.lastReport = %q, want no-replay fallback with crash detail", agent.lastReport)
	}
	if len(agent.reportRequesters) != 0 {
		t.Fatalf("agent.reportRequesters = %v, want drained", agent.reportRequesters)
	}
}

func TestRecoveringAgentIgnoresStoppedHookForOldThread(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	agent.state = agentdto.StateRecovering
	consumer := newHookConsumer(svc, silentLogger())

	consumer.handleThreadStopped(context.Background(), threaddto.Stopped{
		ThreadID: "thread-remote",
		AgentID:  "agent-remote",
		Reason:   "recovery_stop",
	})
	if agent.state != agentdto.StateRecovering || agent.remoteThreadID != "thread-remote" {
		t.Fatalf("agent after old stopped hook = state:%q thread:%q, want recovering old thread preserved", agent.state, agent.remoteThreadID)
	}
}

func TestRecoveringAgentStoppedHookForNewThreadWritesFallback(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	agent.state = agentdto.StateRecovering
	agent.reportRequesters = []string{"agent-parent"}
	consumer := newHookConsumer(svc, silentLogger())

	startedAt := time.Now()
	consumer.handleThreadStarted(context.Background(), threaddto.Started{
		ThreadID: "thread-recovered",
		AgentID:  "agent-remote",
		EventHeader: sharedto.EventHeader{
			Timestamp: startedAt,
		},
	})
	if agent.remoteThreadID != "thread-remote" {
		t.Fatalf("remoteThreadID after recovering thread.started = %q, want old thread until recovery commit", agent.remoteThreadID)
	}
	consumer.handleThreadStopped(context.Background(), threaddto.Stopped{
		ThreadID: "thread-recovered",
		AgentID:  "agent-remote",
		Reason:   "recovered_thread_crashed",
	})
	if agent.state != agentdto.StateStopped || agent.remoteThreadID != "thread-recovered" {
		t.Fatalf("agent after new stopped hook = state:%q thread:%q, want stopped recovered thread", agent.state, agent.remoteThreadID)
	}
	if !strings.Contains(agent.lastReport, "without producing a turn report") || len(agent.reportRequesters) != 0 {
		t.Fatalf("fallback/report requesters = report:%q requesters:%v, want drained fallback", agent.lastReport, agent.reportRequesters)
	}
}

func TestRecoveringAgentIgnoresThirdStaleStoppedHook(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	agent.state = agentdto.StateRecovering
	agent.reportRequesters = []string{"agent-parent"}
	consumer := newHookConsumer(svc, silentLogger())

	startedAt := time.Now()
	consumer.handleThreadStarted(context.Background(), threaddto.Started{
		ThreadID: "thread-recovered",
		AgentID:  "agent-remote",
		EventHeader: sharedto.EventHeader{
			Timestamp: startedAt,
		},
	})
	consumer.handleThreadStopped(context.Background(), threaddto.Stopped{
		ThreadID: "thread-stale-third",
		AgentID:  "agent-remote",
		Reason:   "stale_thread_stop",
	})
	if agent.state != agentdto.StateRecovering || agent.remoteThreadID != "thread-remote" || agent.lastReport != "" {
		t.Fatalf("agent after stale third stopped hook = state:%q thread:%q report:%q, want recovery unchanged", agent.state, agent.remoteThreadID, agent.lastReport)
	}

	consumer.handleThreadStopped(context.Background(), threaddto.Stopped{
		ThreadID: "thread-recovered",
		AgentID:  "agent-remote",
		Reason:   "recovered_thread_crashed",
	})
	if agent.state != agentdto.StateStopped || agent.remoteThreadID != "thread-recovered" {
		t.Fatalf("agent after expected stopped hook = state:%q thread:%q, want stopped recovered thread", agent.state, agent.remoteThreadID)
	}
	if !strings.Contains(agent.lastReport, "without producing a turn report") || len(agent.reportRequesters) != 0 {
		t.Fatalf("fallback/report requesters = report:%q requesters:%v, want drained fallback", agent.lastReport, agent.reportRequesters)
	}
}

func TestRecoveringAgentIgnoresStaleThreadStartedBeforeExpectedThread(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	agent.state = agentdto.StateRecovering
	agent.updatedAt = time.Now()
	agent.reportRequesters = []string{"agent-parent"}
	consumer := newHookConsumer(svc, silentLogger())

	consumer.handleThreadStarted(context.Background(), threaddto.Started{
		ThreadID: "thread-stale-third",
		AgentID:  "agent-remote",
	})
	consumer.handleThreadStarted(context.Background(), threaddto.Started{
		ThreadID: "thread-stale-third",
		AgentID:  "agent-remote",
		EventHeader: sharedto.EventHeader{
			Timestamp: agent.updatedAt.Add(-time.Second),
		},
	})
	consumer.handleThreadStarted(context.Background(), threaddto.Started{
		ThreadID: "thread-recovered",
		AgentID:  "agent-remote",
		EventHeader: sharedto.EventHeader{
			Timestamp: agent.updatedAt.Add(time.Second),
		},
	})
	consumer.handleStateChanged(context.Background(), agentdto.StateChanged{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-stale-third"},
				AgentID:      "agent-remote",
			},
		},
		NewState: string(agentdto.StateFailed),
	})
	if agent.state != agentdto.StateRecovering || agent.remoteThreadID != "thread-remote" || agent.lastReport != "" {
		t.Fatalf("agent after stale started+terminal hook = state:%q thread:%q report:%q, want recovery unchanged", agent.state, agent.remoteThreadID, agent.lastReport)
	}

	consumer.handleStateChanged(context.Background(), agentdto.StateChanged{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-recovered"},
				AgentID:      "agent-remote",
			},
		},
		NewState: string(agentdto.StateFailed),
	})
	if agent.state != agentdto.StateFailed || agent.remoteThreadID != "thread-recovered" {
		t.Fatalf("agent after expected terminal state hook = state:%q thread:%q, want failed recovered thread", agent.state, agent.remoteThreadID)
	}
	if !strings.Contains(agent.lastReport, "without producing a turn report") || len(agent.reportRequesters) != 0 {
		t.Fatalf("fallback/report requesters = report:%q requesters:%v, want drained fallback", agent.lastReport, agent.reportRequesters)
	}
}

func TestRecoveringAgentIgnoresOldStoppedAfterNewThreadStartedHook(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	agent.state = agentdto.StateRecovering
	consumer := newHookConsumer(svc, silentLogger())

	startedAt := time.Now()
	consumer.handleThreadStarted(context.Background(), threaddto.Started{
		ThreadID: "thread-recovered",
		AgentID:  "agent-remote",
		EventHeader: sharedto.EventHeader{
			Timestamp: startedAt,
		},
	})
	if agent.remoteThreadID != "thread-remote" {
		t.Fatalf("remoteThreadID after recovering thread.started = %q, want old thread until recovery commit", agent.remoteThreadID)
	}
	consumer.handleThreadStopped(context.Background(), threaddto.Stopped{
		ThreadID: "thread-remote",
		AgentID:  "agent-remote",
		Reason:   "old_thread_stop_after_new_started",
	})
	if agent.state != agentdto.StateRecovering || agent.remoteThreadID != "thread-remote" || agent.lastReport != "" {
		t.Fatalf("agent after reordered old stopped hook = state:%q thread:%q report:%q, want recovery unchanged", agent.state, agent.remoteThreadID, agent.lastReport)
	}
}

func TestRecoveringAgentIgnoresOldStateChangedAfterNewThreadStartedHook(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	agent.state = agentdto.StateRecovering
	consumer := newHookConsumer(svc, silentLogger())

	startedAt := time.Now()
	consumer.handleThreadStarted(context.Background(), threaddto.Started{
		ThreadID: "thread-recovered",
		AgentID:  "agent-remote",
		EventHeader: sharedto.EventHeader{
			Timestamp: startedAt,
		},
	})
	consumer.handleThreadStarted(context.Background(), threaddto.Started{
		ThreadID: "thread-stale-third",
		AgentID:  "agent-remote",
		EventHeader: sharedto.EventHeader{
			Timestamp: startedAt.Add(time.Second),
		},
	})
	consumer.handleStateChanged(context.Background(), agentdto.StateChanged{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-remote"},
				AgentID:      "agent-remote",
			},
		},
		NewState: string(agentdto.StateFailed),
	})
	if agent.state != agentdto.StateRecovering || agent.remoteThreadID != "thread-remote" || agent.lastReport != "" {
		t.Fatalf("agent after reordered old state hook = state:%q thread:%q report:%q, want recovery unchanged", agent.state, agent.remoteThreadID, agent.lastReport)
	}
}

func TestRecoveringAgentTerminalStateChangedForNewThreadWritesFallback(t *testing.T) {
	t.Parallel()

	for _, nextState := range []agentdto.AgentState{agentdto.StateFailed, agentdto.StateStopped} {
		t.Run(string(nextState), func(t *testing.T) {
			t.Parallel()
			svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
			agent := launcherRecoveryAgent(svc, "agent-remote")
			agent.state = agentdto.StateRecovering
			agent.reportRequesters = []string{"agent-parent"}
			consumer := newHookConsumer(svc, silentLogger())

			startedAt := time.Now()
			consumer.handleThreadStarted(context.Background(), threaddto.Started{
				ThreadID: "thread-recovered",
				AgentID:  "agent-remote",
				EventHeader: sharedto.EventHeader{
					Timestamp: startedAt,
				},
			})
			if agent.remoteThreadID != "thread-remote" {
				t.Fatalf("remoteThreadID after recovering thread.started = %q, want old thread until recovery commit", agent.remoteThreadID)
			}
			consumer.handleStateChanged(context.Background(), agentdto.StateChanged{
				AgentSessionHeader: sharedto.AgentSessionHeader{
					AgentHeader: sharedto.AgentHeader{
						ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-recovered"},
						AgentID:      "agent-remote",
					},
				},
				NewState: string(nextState),
			})
			if !terminalFailedOrStopped(agent.state) || agent.remoteThreadID != "thread-recovered" {
				t.Fatalf("agent after recovered terminal state hook = state:%q thread:%q, want terminal recovered thread", agent.state, agent.remoteThreadID)
			}
			if !strings.Contains(agent.lastReport, "without producing a turn report") || len(agent.reportRequesters) != 0 {
				t.Fatalf("fallback/report requesters = report:%q requesters:%v, want drained fallback", agent.lastReport, agent.reportRequesters)
			}
		})
	}
}

func TestRecoveringAgentIgnoresThirdStaleTerminalStateChanged(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	agent.state = agentdto.StateRecovering
	agent.reportRequesters = []string{"agent-parent"}
	consumer := newHookConsumer(svc, silentLogger())

	startedAt := time.Now()
	consumer.handleThreadStarted(context.Background(), threaddto.Started{
		ThreadID: "thread-recovered",
		AgentID:  "agent-remote",
		EventHeader: sharedto.EventHeader{
			Timestamp: startedAt,
		},
	})
	consumer.handleStateChanged(context.Background(), agentdto.StateChanged{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-stale-third"},
				AgentID:      "agent-remote",
			},
		},
		NewState: string(agentdto.StateFailed),
	})
	if agent.state != agentdto.StateRecovering || agent.remoteThreadID != "thread-remote" || agent.lastReport != "" {
		t.Fatalf("agent after stale third terminal state hook = state:%q thread:%q report:%q, want recovery unchanged", agent.state, agent.remoteThreadID, agent.lastReport)
	}

	consumer.handleStateChanged(context.Background(), agentdto.StateChanged{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-recovered"},
				AgentID:      "agent-remote",
			},
		},
		NewState: string(agentdto.StateFailed),
	})
	if agent.state != agentdto.StateFailed || agent.remoteThreadID != "thread-recovered" {
		t.Fatalf("agent after expected terminal state hook = state:%q thread:%q, want failed recovered thread", agent.state, agent.remoteThreadID)
	}
	if !strings.Contains(agent.lastReport, "without producing a turn report") || len(agent.reportRequesters) != 0 {
		t.Fatalf("fallback/report requesters = report:%q requesters:%v, want drained fallback", agent.lastReport, agent.reportRequesters)
	}
}

func TestStaleStateChangedHookDoesNotOverwriteCurrentThread(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	agent.threadID = "thread-current"
	agent.remoteThreadID = "thread-current"
	consumer := newHookConsumer(svc, silentLogger())

	consumer.handleStateChanged(context.Background(), agentdto.StateChanged{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-old"},
				AgentID:      "agent-remote",
			},
		},
		NewState: string(agentdto.StateFailed),
	})
	if agent.state != agentdto.StateTurnRunning || agent.remoteThreadID != "thread-current" {
		t.Fatalf("agent after stale state hook = state:%q thread:%q, want current runtime unchanged", agent.state, agent.remoteThreadID)
	}
}

func TestTerminalStateChangedWithoutReportWritesFallback(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	agent.reportRequesters = []string{"agent-parent"}
	consumer := newHookConsumer(svc, silentLogger())

	consumer.handleStateChanged(context.Background(), agentdto.StateChanged{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-remote"},
				AgentID:      "agent-remote",
			},
		},
		NewState: string(agentdto.StateFailed),
	})
	if agent.state != agentdto.StateFailed || agent.activeTurnID != "" {
		t.Fatalf("agent after failed state hook = state:%q active:%q, want failed with no active turn", agent.state, agent.activeTurnID)
	}
	if !strings.Contains(agent.lastReport, "without producing a turn report") || len(agent.reportRequesters) != 0 {
		t.Fatalf("fallback/report requesters = report:%q requesters:%v, want drained fallback", agent.lastReport, agent.reportRequesters)
	}
}

func launcherRecoveryAgent(svc *service, agentID string) *agentRuntime {
	agent := svc.newAgentLocked(agentID)
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-remote"
	agent.remoteThreadID = "thread-remote"
	agent.activeTurnID = "turn-active"
	agent.updatedAt = time.Now().Add(-time.Minute)
	svc.registry.agents[agent.id] = agent
	return agent
}

func launcherReplayStore(t *testing.T, agentID string) stubRecoveryTurnStore {
	t.Helper()
	return stubRecoveryTurnStore{
		nodes: []taskdag.Node{{
			DagKey:         "dag-1",
			NodeKey:        "node-1",
			AssignedTo:     agentID,
			ActiveTurnID:   testStringPtr("turn-active"),
			ActiveWakeupID: testInt64Ptr(7),
		}},
		wakeups: map[int64]taskdag.Wakeup{7: {
			ID:            7,
			Status:        "sent",
			TargetAgentID: agentID,
			BoundTurnID:   testStringPtr("turn-active"),
			TurnBoundAt:   testTimePtr(t),
			PromptPayload: mustReplayPayload(agentID),
		}},
	}
}

func mustReplayPayload(agentID string) []byte {
	return []byte(`{"agentId":"` + strings.TrimSpace(agentID) + `","input":[{"type":"text","content":"replay"}]}`)
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}

func assertFailedRecoveryFallback(t *testing.T, agent *agentRuntime, wantDetail string) {
	t.Helper()
	if agent.state != agentdto.StateFailed {
		t.Fatalf("agent.state = %q, want failed", agent.state)
	}
	if !strings.Contains(agent.lastReport, wantDetail) || !strings.Contains(agent.lastReport, "without producing a turn report") {
		t.Fatalf("agent.lastReport = %q, want fallback containing %q", agent.lastReport, wantDetail)
	}
	if len(agent.reportRequesters) != 0 {
		t.Fatalf("agent.reportRequesters = %v, want drained", agent.reportRequesters)
	}
}
