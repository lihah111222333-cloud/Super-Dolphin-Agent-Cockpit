package orchestration

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kelindar/event"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

func TestProviderTurnStartFenceRejectsStopAfterSessionReadyWait(t *testing.T) {
	waitStarted := make(chan struct{})
	waitRelease := make(chan struct{})
	starter := &providerStartFenceStarter{
		waitStarted: waitStarted,
		waitRelease: waitRelease,
	}
	launcher := &providerStartFenceLauncher{}
	svc, agent, work := newProviderStartFenceService(starter, launcher)

	executionDone := make(chan struct{})
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		defer close(executionDone)
		svc.startTurnExecution(context.Background(), work)
	})

	<-waitStarted
	if err := svc.StopAgent(context.Background(), agent.id); err != nil {
		t.Fatalf("StopAgent() error = %v", err)
	}
	if got := starter.startCalls.Load(); got != 0 {
		t.Fatalf("StartTurn calls before ready release = %d, want 0", got)
	}
	close(waitRelease)
	<-executionDone

	if got := starter.startCalls.Load(); got != 0 {
		t.Fatalf("StartTurn calls after completed Stop = %d, want 0", got)
	}
}

func TestProviderTurnStartFenceCompensatesLateSuccessAfterGenerationChange(t *testing.T) {
	startEntered := make(chan struct{})
	startRelease := make(chan struct{})
	starter := &providerStartFenceStarter{
		startEntered:   startEntered,
		startRelease:   startRelease,
		providerTurnID: "provider-turn-old",
	}
	launcher := &providerStartFenceLauncher{}
	svc, agent, work := newProviderStartFenceService(starter, launcher)

	executionDone := make(chan struct{})
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		defer close(executionDone)
		svc.startTurnExecution(context.Background(), work)
	})

	<-startEntered
	if err := svc.StopAgent(context.Background(), agent.id); err != nil {
		t.Fatalf("StopAgent() error = %v", err)
	}
	simulateProviderStartFenceRelaunch(t, svc, agent.id)
	close(startRelease)
	<-executionDone

	assertProviderStartFenceCompensation(t, svc, agent.id, launcher)
}

func simulateProviderStartFenceRelaunch(t *testing.T, svc *service, agentID string) {
	t.Helper()
	if err := svc.registry.withAgentLocked(agentID, func(current *agentRuntime) error {
		current.state = agentdto.StateIdle
		current.stopRequested = false
		current.launchSeq = 2
		current.sessionGeneration = 2
		current.remoteThreadID = "thread-new"
		return nil
	}); err != nil {
		t.Fatalf("simulate relaunch generation: %v", err)
	}
}

func assertProviderStartFenceCompensation(
	t *testing.T,
	svc *service,
	agentID string,
	launcher *providerStartFenceLauncher,
) {
	t.Helper()
	interruptCalls, stopCalls, interruptedThreadID, interruptedTurnID := launcher.snapshot()
	if interruptCalls != 1 {
		t.Fatalf("compensating Interrupt calls = %d, want 1", interruptCalls)
	}
	if stopCalls != 1 {
		t.Fatalf("Stop calls = %d, want only the user Stop call", stopCalls)
	}
	if interruptedThreadID != "thread-old" || interruptedTurnID != "provider-turn-old" {
		t.Fatalf("compensating interrupt target = %q/%q, want old thread/provider turn", interruptedThreadID, interruptedTurnID)
	}
	if handled := svc.turns.deferProviderTurnCompletion(turndto.TurnCompleted{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader: shareddto.AgentHeader{
				ThreadHeader: shareddto.ThreadHeader{ThreadID: "thread-old"},
				AgentID:      agentID,
			},
			TurnIDHeader: shareddto.TurnIDHeader{TurnID: "provider-turn-old"},
		},
		Success: false,
	}); handled {
		t.Fatal("late provider terminal was buffered after compensation")
	}
	assertProviderStartFenceCurrentGenerationIdle(t, svc, agentID)
}

func assertProviderStartFenceCurrentGenerationIdle(t *testing.T, svc *service, agentID string) {
	t.Helper()
	if err := svc.registry.withAgentReadLocked(agentID, func(current *agentRuntime) error {
		if current.state != agentdto.StateIdle || current.activeTurnID != "" {
			t.Fatalf("current generation state = %q active turn = %q, want idle without orphan turn", current.state, current.activeTurnID)
		}
		if current.pendingProviderTurnID != "" || current.pendingProviderTerminal != nil {
			t.Fatalf("pending provider state = %q/%#v, want cleared", current.pendingProviderTurnID, current.pendingProviderTerminal)
		}
		return nil
	}); err != nil {
		t.Fatalf("read current generation: %v", err)
	}
}

func newProviderStartFenceService(starter *providerStartFenceStarter, launcher *providerStartFenceLauncher) (*service, *agentRuntime, turnWork) {
	svc := NewService(silentLogger(), event.NewDispatcher(), launcher, nil, starter, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnStarting
	agent.activeTurnID = "turn-local"
	agent.remoteThreadID = "thread-old"
	agent.launchSeq = 1
	agent.sessionGeneration = 1
	svc.registry.agents[agent.id] = agent
	work := turnWork{
		agentID:    agent.id,
		threadID:   "thread-old",
		turnID:     "turn-local",
		submission: TurnSubmission{AgentID: agent.id, ThreadID: "thread-old", ExpectedTurnID: "turn-local"},
	}
	return svc, agent, work
}

type providerStartFenceStarter struct {
	startCalls     atomic.Int64
	waitStarted    chan struct{}
	waitRelease    chan struct{}
	startEntered   chan struct{}
	startRelease   chan struct{}
	providerTurnID string
}

func (s *providerStartFenceStarter) StartTurn(context.Context, TurnSubmission) (string, error) {
	s.startCalls.Add(1)
	if s.startEntered != nil {
		close(s.startEntered)
	}
	if s.startRelease != nil {
		<-s.startRelease
	}
	return s.providerTurnID, nil
}

func (s *providerStartFenceStarter) WaitForSessionReady(context.Context, string, time.Duration) error {
	if s.waitStarted != nil {
		close(s.waitStarted)
	}
	if s.waitRelease != nil {
		<-s.waitRelease
	}
	return nil
}

type providerStartFenceLauncher struct {
	mu                  sync.Mutex
	interruptCalls      int
	stopCalls           int
	interruptedThreadID string
	interruptedTurnID   string
}

func (*providerStartFenceLauncher) Launch(context.Context, *agentRuntime, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, nil
}

func (*providerStartFenceLauncher) Fork(context.Context, *agentRuntime, *agentRuntime, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, nil
}

func (l *providerStartFenceLauncher) Stop(context.Context, *agentRuntime) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopCalls++
	return nil
}

func (*providerStartFenceLauncher) Archive(context.Context, *agentRuntime) error { return nil }

func (l *providerStartFenceLauncher) Interrupt(_ context.Context, agent *agentRuntime, _ string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.interruptCalls++
	l.interruptedThreadID = agent.remoteThreadID
	l.interruptedTurnID = agent.activeTurnID
	return nil
}

func (*providerStartFenceLauncher) SubmitTurn(context.Context, *agentRuntime, TurnSubmission) (string, error) {
	return "", nil
}

func (*providerStartFenceLauncher) IsRunning(context.Context, *agentRuntime) bool { return true }

func (l *providerStartFenceLauncher) snapshot() (int, int, string, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.interruptCalls, l.stopCalls, l.interruptedThreadID, l.interruptedTurnID
}
