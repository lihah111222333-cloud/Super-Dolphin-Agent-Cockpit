package claudecli

import (
	"context"
	"errors"
	"testing"
	"time"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestInterruptCancelsPendingRestart(t *testing.T) {
	next := newScriptedTransport()
	defer next.finish()
	launchFn := overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		return next.tr, nil, nil
	})

	oldReady := make(chan struct{})
	close(oldReady)
	s := assumeSessionLaunchOverride(&session{
		threadID:        "pending",
		sessionID:       "pending",
		threadReady:     oldReady,
		transport:       closedTransport(),
		launchCLI:       launchFn,
		suppressedTurns: map[string]struct{}{},
		model:           "claude-old",
		config:          cliLaunchConfig{PromptSnapshot: validResumePromptSnapshotForTest()},
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		_, err := s.StartTurn(ctx, turnRequest("claude-new"))
		result <- err
	})

	_ = waitForReadySwap(t, s, oldReady)
	if err := s.Interrupt(context.Background(), dto.InterruptRequest{Source: "ui_stop"}); err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("StartTurn() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("StartTurn() did not unblock after interrupt")
	}
	tr, handle := sessionStateForInterruptTest(s)
	if tr != nil {
		t.Fatal("session transport was not cleared after pending restart interrupt")
	}
	if handle != nil {
		t.Fatal("active turn should not be created after pending restart interrupt")
	}
	select {
	case write := <-next.stdin.writes:
		t.Fatalf("StartTurn wrote after interrupt: %q", write)
	case <-time.After(100 * time.Millisecond):
	}
}
