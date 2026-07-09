package orchestration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/exitmonitor"
	"github.com/kelindar/event"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
)

type stopTestSessionCleaner struct {
	removeCurrentCalls int
	removeGeneration   []uint64
}

func (c *stopTestSessionCleaner) RemoveSession(string) {
	c.removeCurrentCalls++
}

func (c *stopTestSessionCleaner) RemoveSessionGeneration(_ string, generation uint64) {
	c.removeGeneration = append(c.removeGeneration, generation)
}

func TestStopAgentPublishesStoppedAfterObservedExit(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	stopped := make(chan agentdto.AgentStopped, 1)
	cancel := event.Subscribe(dispatcher, func(ev agentdto.AgentStopped) {
		stopped <- ev
	})
	defer cancel()

	cleaner := &stopTestSessionCleaner{}
	svc := NewService(silentLogger(), dispatcher, nil, cleaner, nil, nil)
	cmd := newLongRunningTestCommand()
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start() error = %v", err)
	}

	agent := svc.newAgentLocked("agent-1")
	agent.cmd = cmd
	agent.state = agentdto.StateIdle
	agent.threadID = "thread-1"
	agent.launchSeq = 1
	agent.sessionGeneration = 7
	svc.registry.agents[agent.id] = agent

	waitDone := make(chan struct{})
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		err := cmd.Wait()
		svc.handleProcessExit(context.Background(), agent.id, 1, err)
		close(waitDone)
	})

	if err := svc.StopAgent(context.Background(), agent.id); err != nil {
		t.Fatalf("StopAgent() error = %v", err)
	}

	waitForStopTestProcessExit(t, waitDone)
	requireStopTestEvent(t, stopped, "user_requested", "agent-1")

	if agent.state != agentdto.StateStopped {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateStopped)
	}
	if agent.cmd != nil {
		t.Fatal("agent.cmd = non-nil, want nil after observed exit")
	}
	if cleaner.removeCurrentCalls != 0 {
		t.Fatalf("removeCurrentCalls = %d, want 0", cleaner.removeCurrentCalls)
	}
	if len(cleaner.removeGeneration) != 1 || cleaner.removeGeneration[0] != 7 {
		t.Fatalf("removeGeneration = %#v, want [7]", cleaner.removeGeneration)
	}
}

func TestStopAllAgentsPublishesShutdownAfterObservedExit(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	stopped := make(chan agentdto.AgentStopped, 1)
	cancel := event.Subscribe(dispatcher, func(ev agentdto.AgentStopped) {
		if ev.AgentID == "agent-1" {
			stopped <- ev
		}
	})
	defer cancel()

	cleaner := &stopTestSessionCleaner{}
	svc := NewService(silentLogger(), dispatcher, nil, cleaner, nil, nil)
	cmd := newLongRunningTestCommand()
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start() error = %v", err)
	}

	agent := svc.newAgentLocked("agent-1")
	agent.cmd = cmd
	agent.state = agentdto.StateIdle
	agent.launchSeq = 1
	agent.sessionGeneration = 9
	svc.registry.agents[agent.id] = agent

	waitDone := make(chan struct{})
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		err := cmd.Wait()
		svc.handleProcessExit(context.Background(), agent.id, 1, err)
		close(waitDone)
	})

	svc.StopAllAgents(context.Background())

	waitForStopTestProcessExit(t, waitDone)
	requireStopTestEvent(t, stopped, "shutdown", "")

	if agent.cmd != nil {
		t.Fatal("agent.cmd = non-nil, want nil after observed exit")
	}
	if len(cleaner.removeGeneration) != 1 || cleaner.removeGeneration[0] != 9 {
		t.Fatalf("removeGeneration = %#v, want [9]", cleaner.removeGeneration)
	}
}

func TestStopAllAgentsReturnsAfterWaitTimeout(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.lifecycle.processExitWaitTimeout = 25 * time.Millisecond
	cmd := newLongRunningTestCommand()
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start() error = %v", err)
	}
	defer func() { _ = cmd.Wait() }()

	agent := svc.newAgentLocked("agent-1")
	agent.cmd = cmd
	agent.state = agentdto.StateIdle
	agent.launchSeq = 1
	svc.registry.agents[agent.id] = agent

	done := make(chan struct{})
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		svc.StopAllAgents(context.Background())
		close(done)
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("StopAllAgents() did not return after process exit wait timeout")
	}
}

func TestStopAllAgentsHonorsTotalShutdownDeadline(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	blocker := make(chan struct{})
	svc.lifecycle.asyncWg.Add(1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		defer svc.lifecycle.asyncWg.Done()
		<-blocker
	})
	defer close(blocker)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	svc.StopAllAgents(ctx)
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("StopAllAgents(ctx) elapsed = %s, want bounded by shutdown context", elapsed)
	}
}

func TestWaitForProcessExitReturnsErrorWhenForceKillFails(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.lifecycle.processExitWaitTimeout = 25 * time.Millisecond
	agent := svc.newAgentLocked("agent-1")
	agent.cmd = &exec.Cmd{Process: &os.Process{Pid: -1}}
	agent.launchSeq = 1
	svc.registry.agents[agent.id] = agent

	if err := svc.lifecycle.waitForProcessExit(context.Background(), svc.registry, svc.logger, agent.id, agent.launchSeq); err == nil {
		t.Fatal("waitForProcessExit() error = nil, want force-kill failure")
	}
}

func TestRunnerActorShutdownObservesProcessExitAfterContextCancel(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	stopped := make(chan agentdto.AgentStopped, 1)
	cancelSubscription := event.Subscribe(dispatcher, func(ev agentdto.AgentStopped) {
		if ev.AgentID == "agent-1" {
			stopped <- ev
		}
	})
	defer cancelSubscription()

	cleaner := &stopTestSessionCleaner{}
	svc := NewService(silentLogger(), dispatcher, nil, cleaner, nil, nil)
	cmd := newLongRunningTestCommand()
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start() error = %v", err)
	}

	agent := svc.newAgentLocked("agent-1")
	agent.cmd = cmd
	agent.state = agentdto.StateIdle
	agent.launchSeq = 1
	agent.sessionGeneration = 13
	svc.registry.agents[agent.id] = agent
	// 本测试手动构造 cmd 和 agentRuntime，绕过 startProcessLocked。
	// 生产路径会在 startProcessLocked 内 arm exit monitor；这里必须镜像该动作，runner 才能观察 cmd.Wait。
	svc.lifecycle.exitMonitor.Arm(exitmonitor.Target{
		AgentID:   agent.id,
		LaunchSeq: agent.launchSeq,
		Cmd:       cmd,
	})
	agent.monitoredSeq = agent.launchSeq

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		runDone <- newRunnerActorForTest(silentLogger(), svc).Run(ctx)
	})

	waitForAgentMonitor(t, svc, agent.id, agent.launchSeq)
	cancel()

	requireRunnerStoppedAfterCancel(t, runDone)
	requireStopTestEvent(t, stopped, "shutdown", "")

	svc.registry.mu.RLock()
	exited := svc.registry.agents[agent.id].lastExitedSeq >= 1
	svc.registry.mu.RUnlock()
	if !exited {
		t.Fatal("lastExitedSeq was not observed after runner shutdown")
	}
	if agent.cmd != nil {
		t.Fatal("agent.cmd = non-nil, want nil after shutdown exit observation")
	}
	if len(cleaner.removeGeneration) != 1 || cleaner.removeGeneration[0] != 13 {
		t.Fatalf("removeGeneration = %#v, want [13]", cleaner.removeGeneration)
	}
}

func waitForStopTestProcessExit(t *testing.T, waitDone <-chan struct{}) {
	t.Helper()
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("process exit was not observed")
	}
}

func requireStopTestEvent(t *testing.T, stopped <-chan agentdto.AgentStopped, wantReason, wantAgentID string) {
	t.Helper()
	select {
	case ev := <-stopped:
		if ev.Reason != wantReason {
			t.Fatalf("AgentStopped reason = %q, want %s", ev.Reason, wantReason)
		}
		if wantAgentID != "" && ev.AgentID != wantAgentID {
			t.Fatalf("AgentStopped agent_id = %q, want %s", ev.AgentID, wantAgentID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected AgentStopped event")
	}
}

func requireRunnerStoppedAfterCancel(t *testing.T, runDone <-chan error) {
	t.Helper()
	select {
	case err := <-runDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop after context cancel")
	}
}

func TestRequestAgentStopKeepsOriginalReasonOnRepeat(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	svc.registry.agents[agent.id] = agent

	if _, err := svc.lifecycle.requestAgentStop(context.Background(), svc.registry, agent.id, "shutdown", svc); err != nil {
		t.Fatalf("requestAgentStop(first) error = %v", err)
	}
	if _, err := svc.lifecycle.requestAgentStop(context.Background(), svc.registry, agent.id, "user_requested", svc); err != nil {
		t.Fatalf("requestAgentStop(second) error = %v", err)
	}
	if !agent.stopRequested {
		t.Fatal("agent.stopRequested = false, want true")
	}
	if agent.stopReason != "shutdown" {
		t.Fatalf("agent.stopReason = %q, want shutdown", agent.stopReason)
	}
}

func TestRemoveSessionGenerationAwareCleanerDoesNotFallbackToCurrent(t *testing.T) {
	t.Parallel()

	cleaner := &stopTestSessionCleaner{}
	svc := NewService(silentLogger(), nil, nil, cleaner, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.sessionGeneration = 11

	svc.removeSession(agent)
	svc.removeSession(agent)

	if cleaner.removeCurrentCalls != 0 {
		t.Fatalf("removeCurrentCalls = %d, want 0", cleaner.removeCurrentCalls)
	}
	if len(cleaner.removeGeneration) != 1 || cleaner.removeGeneration[0] != 11 {
		t.Fatalf("removeGeneration = %#v, want [11]", cleaner.removeGeneration)
	}
}

func TestHandleProcessExitClearsRuntimeState(t *testing.T) {
	t.Parallel()

	svc := NewService(nil, nil, nil, nil, nil, nil)
	agent := runtimeTestAgent()
	agent.state = agentdto.StateStopping
	agent.stopRequested = true
	agent.stopReason = "user_requested"
	agent.launchSeq = 2
	agent.runtimePort = 9090
	agent.runtimeProvider = "claude"
	svc.registry.agents[agent.id] = agent

	svc.handleProcessExit(context.Background(), agent.id, 2, nil)

	snapshot := svc.snapshotLocked(context.Background(), agent)
	if snapshot.Port != 8080 || snapshot.PortSource != "inferred" {
		t.Fatalf("snapshot port after exit = (%d, %q), want (8080, inferred)", snapshot.Port, snapshot.PortSource)
	}
	if snapshot.Provider != "codex" || snapshot.ProviderSource != "inferred" {
		t.Fatalf("snapshot provider after exit = (%q, %q), want (codex, inferred)", snapshot.Provider, snapshot.ProviderSource)
	}
	if agent.runtimePort != 0 {
		t.Fatalf("agent.runtimePort = %d, want 0", agent.runtimePort)
	}
	if agent.runtimeProvider != "" {
		t.Fatalf("agent.runtimeProvider = %q, want empty", agent.runtimeProvider)
	}
}

func waitForAgentMonitor(t *testing.T, svc *service, agentID string, launchSeq uint64) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		svc.registry.mu.RLock()
		agent, ok := svc.registry.agents[agentID]
		ready := ok && agent.monitoredSeq >= launchSeq
		svc.registry.mu.RUnlock()
		if ready {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("agent %q was not monitored for launch seq %d", agentID, launchSeq)
}
