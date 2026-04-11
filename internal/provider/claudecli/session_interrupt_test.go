package claudecli

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func overrideSettleInterruptedTransport(t *testing.T, fn func(*transport) error) {
	t.Helper()
	prev := settleInterruptedTransport
	settleInterruptedTransport = fn
	t.Cleanup(func() {
		settleInterruptedTransport = prev
	})
}

func newInterruptTestTransport(t *testing.T, script string) *transport {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("interrupt transport test requires POSIX signals")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	tr, err := newTransport(shell, []string{"-c", script}, "", nil)
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	t.Cleanup(func() { _ = tr.Kill() })
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if tr.Running() {
			return tr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("transport did not start running")
	return nil
}

func sessionStateForInterruptTest(s *session) (*transport, *turnHandle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transport, s.activeTurn
}

func runInterruptRestartScenario(t *testing.T, script string) {
	t.Helper()
	overrideSettleInterruptedTransport(t, func(tr *transport) error {
		return settleInterruptedTransportWithTimeout(tr, 50*time.Millisecond)
	})
	next := newScriptedTransport()
	defer next.finish()
	resumeIDs := make(chan string, 1)
	overrideLaunchCLI(t, func(_, _, _, _ string, _ cliLaunchConfig, _ dto.MCPManifest, resumeID string) (*transport, func(), error) {
		resumeIDs <- resumeID
		return next.tr, nil, nil
	})
	oldReady := make(chan struct{})
	close(oldReady)
	oldTransport := newInterruptTestTransport(t, script)
	cleanupCalled := false
	active := newTurnHandle("local-1", "turn-1")
	s := &session{
		threadID:        "thread-1",
		sessionID:       "thread-1",
		publicThreadID:  "thread-public",
		threadReady:     oldReady,
		transport:       oldTransport,
		cleanup:         func() { cleanupCalled = true },
		activeTurn:      active,
		suppressedTurns: map[string]struct{}{},
		model:           "claude-old",
	}
	if err := s.Interrupt(context.Background(), dto.InterruptRequest{Source: "ui_stop"}); err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}
	select {
	case <-active.Done():
	default:
		t.Fatal("Interrupt() did not finish active turn")
	}
	if !errors.Is(active.Err(), context.Canceled) {
		t.Fatalf("active.Err() = %v, want context.Canceled", active.Err())
	}
	if !cleanupCalled {
		t.Fatal("interrupt cleanup was not called")
	}
	if oldTransport.Running() {
		t.Fatal("old transport still running after interrupt cleanup")
	}
	tr, handle := sessionStateForInterruptTest(s)
	if tr != nil {
		t.Fatal("session transport was not cleared after interrupt")
	}
	if handle != nil {
		t.Fatal("active turn was not cleared after interrupt")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	newHandle, err := s.StartTurn(ctx, turnRequest("claude-next"))
	if err != nil {
		t.Fatalf("StartTurn() after interrupt error = %v", err)
	}
	if newHandle == nil {
		t.Fatal("StartTurn() after interrupt returned nil handle")
	}
	select {
	case write := <-next.stdin.writes:
		if !strings.Contains(write, "hello") {
			t.Fatalf("transport write = %q, want turn payload", write)
		}
	case <-time.After(time.Second):
		t.Fatal("StartTurn() did not write payload after interrupt restart")
	}
	_, _, ready := snapshotSessionState(s)
	select {
	case <-ready:
	default:
		t.Fatal("restart did not reuse known thread readiness")
	}
	select {
	case resumeID := <-resumeIDs:
		if resumeID != "thread-1" {
			t.Fatalf("resumeID = %q, want thread-1", resumeID)
		}
	default:
		t.Fatal("restart launch was not invoked")
	}
}

func TestInterruptExitedCLIAllowsNextTurn(t *testing.T) {
	runInterruptRestartScenario(t, "while :; do sleep 1; done")
}

func TestInterruptHungCLIKillsAndAllowsNextTurn(t *testing.T) {
	runInterruptRestartScenario(t, "trap '' INT; while :; do sleep 1; done")
}
