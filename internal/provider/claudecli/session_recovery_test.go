package claudecli

import (
	"context"
	"strings"
	"testing"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestStartTurnRestartsUnavailableTransportWithoutSettingsChange(t *testing.T) {
	next := newScriptedTransport()
	defer next.finish()
	resumeIDs := make(chan string, 1)
	overrideLaunchCLI(t, func(_, _, _, _ string, _ cliLaunchConfig, _ dto.MCPManifest, resumeID string) (*transport, func(), error) {
		resumeIDs <- resumeID
		return next.tr, nil, nil
	})

	oldReady := make(chan struct{})
	close(oldReady)
	s := &session{
		threadID:        "thread-dead",
		sessionID:       "thread-dead",
		threadReady:     oldReady,
		transport:       closedTransport(),
		suppressedTurns: map[string]struct{}{},
		model:           "claude-same",
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results := make(chan startTurnResult, 1)
	go func() {
		handle, err := s.StartTurn(ctx, turnRequest("claude-same"))
		results <- startTurnResult{handle: handle, err: err}
	}()

	ready := waitForReadySwap(t, s, oldReady)
	next.emitSystemInit(t, "thread-dead")
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("replacement ready channel did not close")
	}
	select {
	case write := <-next.stdin.writes:
		if !strings.Contains(write, "hello") {
			t.Fatalf("transport write = %q, want turn payload", write)
		}
	case <-time.After(time.Second):
		t.Fatal("StartTurn did not write after transport recovery")
	}
	select {
	case res := <-results:
		if res.err != nil || res.handle == nil {
			t.Fatalf("StartTurn result = handle:%v err:%v, want success", res.handle, res.err)
		}
	case <-time.After(time.Second):
		t.Fatal("StartTurn did not finish after transport recovery")
	}
	select {
	case resumeID := <-resumeIDs:
		if resumeID != "thread-dead" {
			t.Fatalf("resumeID = %q, want thread-dead", resumeID)
		}
	default:
		t.Fatal("restart launch was not invoked")
	}
}
