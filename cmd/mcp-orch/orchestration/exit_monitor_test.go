package orchestration

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/exitmonitor"
	"github.com/kelindar/event"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
)

// -----------------------------------------------------------------------------
// Helpers shared across P22 P3 exit-monitor tests.
// -----------------------------------------------------------------------------

// newP3TestService builds a bare service wired with a dispatcher + session
// cleaner. Callers inject agents via svc.agents[...] before exercising the
// runner actor. Parallel-safe because each call returns a fresh service.
func newP3TestService(t *testing.T) (*service, *event.Dispatcher, *stopTestSessionCleaner) {
	t.Helper()
	dispatcher := event.NewDispatcher()
	cleaner := &stopTestSessionCleaner{}
	svc := NewService(silentLogger(), dispatcher, nil, cleaner, nil, nil)
	return svc, dispatcher, cleaner
}

// spawnP3TestCmd starts a short-lived sleep subprocess registered with the
// exit monitor so tests can force a real cmd.Wait path without waiting for
// the full 30s fallback.
func spawnP3TestCmd(t *testing.T, svc *service, agentID string, launchSeq uint64) *exec.Cmd {
	t.Helper()
	cmd := newLongRunningTestCommand()
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start(): %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	agent := svc.newAgentLocked(agentID)
	agent.cmd = cmd
	agent.state = agentdto.StateIdle
	agent.launchSeq = launchSeq
	agent.sessionGeneration = 42
	svc.agents[agent.id] = agent
	svc.exitMonitor.Arm(exitmonitor.Target{AgentID: agentID, LaunchSeq: launchSeq, Cmd: cmd})
	agent.monitoredSeq = launchSeq
	return cmd
}

// -----------------------------------------------------------------------------
// Test 1: exit events are exactly-once per (agentID, launchSeq)
// -----------------------------------------------------------------------------

func TestExitEventExactlyOnceByLaunchSeq(t *testing.T) {
	t.Parallel()
	svc, _, _ := newP3TestService(t)

	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	agent.launchSeq = 5
	svc.agents[agent.id] = agent

	// First Emit lands and triggers the state transition.
	svc.exitMonitor.Emit("agent-1", 5, nil)
	select {
	case result := <-svc.exitMonitor.ExitEvents():
		svc.handleProcessExit(context.Background(), result.AgentID, result.LaunchSeq, result.Err)
	case <-time.After(time.Second):
		t.Fatal("first Emit did not publish exit event")
	}

	svc.mu.RLock()
	lastExited := agent.lastExitedSeq
	svc.mu.RUnlock()
	if lastExited != 5 {
		t.Fatalf("lastExitedSeq after first Emit = %d, want 5", lastExited)
	}

	// Second Emit for the same (agentID, launchSeq) is swallowed by the
	// monitor's fence — the events channel stays empty.
	svc.exitMonitor.Emit("agent-1", 5, errors.New("duplicate"))
	select {
	case ev := <-svc.exitMonitor.ExitEvents():
		t.Fatalf("second Emit should have been coalesced, got event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}

	// Even if a caller bypasses the monitor and calls handleProcessExit
	// directly with the same seq, the per-agent fence in handleProcessExit
	// guards against duplicate state transitions.
	svc.handleProcessExit(context.Background(), "agent-1", 5, errors.New("bypass"))
	svc.mu.RLock()
	lastExitedAfter := agent.lastExitedSeq
	svc.mu.RUnlock()
	if lastExitedAfter != 5 {
		t.Fatalf("lastExitedSeq after duplicate handleProcessExit = %d, want 5", lastExitedAfter)
	}
}

// -----------------------------------------------------------------------------
// Test 2: stop path reuses the same exit owner; no double-transition
// -----------------------------------------------------------------------------

func TestStopPathReusesExitOwner(t *testing.T) {
	t.Parallel()
	svc, _, _ := newP3TestService(t)

	agent := svc.newAgentLocked("agent-2")
	agent.state = agentdto.StateIdle
	agent.launchSeq = 7
	svc.agents[agent.id] = agent

	// Round 1: synthetic launcher stop emits an exit event.
	svc.exitMonitor.Emit("agent-2", 7, nil)
	select {
	case result := <-svc.exitMonitor.ExitEvents():
		svc.handleProcessExit(context.Background(), result.AgentID, result.LaunchSeq, result.Err)
	case <-time.After(time.Second):
		t.Fatal("synthetic Emit did not publish")
	}

	// Round 2: simulate a racing cmd.Wait delivering the same (agentID, seq).
	// The fence + exactly-once channel contract must swallow it.
	svc.exitMonitor.Emit("agent-2", 7, errors.New("cmd.Wait race"))
	select {
	case ev := <-svc.exitMonitor.ExitEvents():
		t.Fatalf("expected no further events, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}

	svc.mu.RLock()
	lastExited := agent.lastExitedSeq
	svc.mu.RUnlock()
	if lastExited != 7 {
		t.Fatalf("lastExitedSeq = %d, want 7", lastExited)
	}
}

// -----------------------------------------------------------------------------
// Test 3: runner shutdown waits for monitor.Drain before returning
// -----------------------------------------------------------------------------

func TestShutdownDrainWaitOwner(t *testing.T) {
	t.Parallel()
	svc, _, _ := newP3TestService(t)
	cmd := spawnP3TestCmd(t, svc, "agent-3", 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- NewRunnerActor(silentLogger(), svc).Run(ctx) }()
	waitForAgentMonitor(t, svc, "agent-3", 1)

	// Cancel the runner ctx — drainOnStop must Drain the monitor before
	// Run returns. Kick the process ourselves so cmd.Wait actually returns;
	// in production this comes from StopAllAgents' SIGTERM path.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = cmd.Process.Kill()
	}()
	cancel()

	select {
	case err := <-runDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runner did not return after drainOnStop")
	}

	// At this point Drain should have joined every Arm goroutine.
	// Second Drain is idempotent and returns immediately.
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer drainCancel()
	if err := svc.exitMonitor.Drain(drainCtx); err != nil {
		t.Fatalf("post-shutdown Drain returned %v; expected quiescent monitor", err)
	}
}

// -----------------------------------------------------------------------------
// Test 4: kill timeout still emits exactly one exit event
// -----------------------------------------------------------------------------

func TestKillTimeoutStillEmitsSingleExitEvent(t *testing.T) {
	t.Parallel()
	svc, _, _ := newP3TestService(t)
	spawnP3TestCmd(t, svc, "agent-4", 3)

	// Shrink processExitWaitTimeout so waitForProcessExit hits its force-kill
	// branch quickly, mirroring the P1c §crash-window contract.
	svc.processExitWaitTimeout = 50 * time.Millisecond

	// Run the actor so exit events get consumed; the process is still alive
	// until waitForProcessExit's forceKill fires.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- NewRunnerActor(silentLogger(), svc).Run(ctx) }()
	waitForAgentMonitor(t, svc, "agent-4", 3)

	if err := svc.waitForProcessExit(context.Background(), "agent-4", 3); err != nil {
		// waitForProcessExit returns nil on successful force-kill; an error
		// means the process was already gone (which is also fine).
		t.Logf("waitForProcessExit returned %v (expected on force-kill path)", err)
	}

	// Wait for lastExitedSeq to advance via the monitor event delivery.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		svc.mu.RLock()
		seq := svc.agents["agent-4"].lastExitedSeq
		svc.mu.RUnlock()
		if seq >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	svc.mu.RLock()
	finalSeq := svc.agents["agent-4"].lastExitedSeq
	svc.mu.RUnlock()
	if finalSeq != 3 {
		t.Fatalf("lastExitedSeq = %d, want 3 (single exit event)", finalSeq)
	}

	// Now a synthetic extra Emit for the same seq must be swallowed — fence
	// on monitor plus fence in handleProcessExit.
	svc.exitMonitor.Emit("agent-4", 3, errors.New("duplicate after kill"))
	select {
	case ev := <-svc.exitMonitor.ExitEvents():
		t.Fatalf("unexpected duplicate event after kill: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	<-runDone
}

// -----------------------------------------------------------------------------
// Test 5: process exit transitions through the state machine once per seq
// -----------------------------------------------------------------------------

// TestProcessExitStateMachine verifies that handleProcessExit is exactly-once
// per (agentID, launchSeq) and that the lastExitedSeq fence prevents a
// duplicate call from re-running the state-machine transition, session
// cleanup, or stopped/failed publishes. The specific target state depends on
// the prior state machine state and is exercised by broader launch_test /
// stop_test flows — here we only assert the exactly-once invariant that P3
// adds at the handler level.
func TestProcessExitStateMachine(t *testing.T) {
	t.Parallel()
	svc, _, cleaner := newP3TestService(t)

	agent := svc.newAgentLocked("agent-5")
	agent.launchSeq = 9
	agent.sessionGeneration = 17
	svc.agents[agent.id] = agent

	svc.handleProcessExit(context.Background(), "agent-5", 9, errors.New("boom"))
	svc.mu.RLock()
	lastExited := agent.lastExitedSeq
	exitedAt := agent.exitedAt
	cmdNil := agent.cmd == nil
	svc.mu.RUnlock()
	if lastExited != 9 {
		t.Fatalf("lastExitedSeq = %d, want 9", lastExited)
	}
	if exitedAt == nil {
		t.Fatal("exitedAt was not set after first handleProcessExit")
	}
	if !cmdNil {
		t.Fatal("agent.cmd should be nil after first handleProcessExit")
	}
	if len(cleaner.removeGeneration) != 1 {
		t.Fatalf("session cleanup calls = %d, want 1", len(cleaner.removeGeneration))
	}

	// Duplicate handleProcessExit on the same (agentID, launchSeq): fence
	// must block the second run — no extra session cleanup, no re-transition,
	// lastExitedSeq unchanged.
	svc.handleProcessExit(context.Background(), "agent-5", 9, errors.New("duplicate"))
	if len(cleaner.removeGeneration) != 1 {
		t.Fatalf("session cleanup calls after duplicate = %d, want 1 (fence broken)", len(cleaner.removeGeneration))
	}
	svc.mu.RLock()
	dupSeq := agent.lastExitedSeq
	svc.mu.RUnlock()
	if dupSeq != 9 {
		t.Fatalf("lastExitedSeq after duplicate = %d, want 9", dupSeq)
	}

	// Stale-seq call (seq older than current) is also a no-op.
	svc.handleProcessExit(context.Background(), "agent-5", 5, nil)
	if len(cleaner.removeGeneration) != 1 {
		t.Fatalf("session cleanup calls after stale = %d, want 1", len(cleaner.removeGeneration))
	}
}

// -----------------------------------------------------------------------------
// Monitor unit-level: Drain rejects late Arm and joins in-flight waits
// -----------------------------------------------------------------------------

func TestExitMonitorDrainClosesGate(t *testing.T) {
	t.Parallel()
	m := exitmonitor.New(silentLogger())

	cmd := newLongRunningTestCommand()
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start(): %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	m.Arm(exitmonitor.Target{AgentID: "a", LaunchSeq: 1, Cmd: cmd})

	// Drain in a goroutine so the test can observe the gate flipping.
	drainDone := make(chan error, 1)
	go func() {
		drainCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		drainDone <- m.Drain(drainCtx)
	}()

	// Kill the cmd so Drain can finish.
	_ = cmd.Process.Kill()

	if err := <-drainDone; err != nil {
		t.Fatalf("Drain err = %v, want nil", err)
	}

	// After Drain, Arm must refuse new targets.
	other := newLongRunningTestCommand()
	if err := other.Start(); err != nil {
		t.Fatalf("cmd.Start(): %v", err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })
	if armed := m.Arm(exitmonitor.Target{AgentID: "b", LaunchSeq: 1, Cmd: other}); armed {
		t.Fatal("Arm after Drain must return false")
	}
}

// -----------------------------------------------------------------------------
// stopTestSessionCleaner may already be declared in stop_test.go; we avoid
// redeclaring by referencing it from this file only through newP3TestService.
// -----------------------------------------------------------------------------

var _ sync.Locker = (*sync.Mutex)(nil) // keep sync import stable for future helpers
