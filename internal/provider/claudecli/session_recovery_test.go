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
	s := assumeSessionLaunchOverride(&session{
		threadID:        "11111111-2222-3333-4444-555555555555",
		sessionID:       "11111111-2222-3333-4444-555555555555",
		threadReady:     oldReady,
		transport:       closedTransport(),
		suppressedTurns: map[string]struct{}{},
		model:           "claude-same",
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results := startTurnAsync(ctx, s, "claude-same")

	ready := waitForReadySwap(t, s, oldReady)
	next.emitSystemInit(t, "11111111-2222-3333-4444-555555555555")
	waitForReplacementReady(t, ready)
	assertTransportWrite(t, next, "hello")
	assertStartTurnSuccess(t, results)
	assertResumeID(t, resumeIDs, "11111111-2222-3333-4444-555555555555")
}

func startTurnAsync(ctx context.Context, s *session, model string) <-chan startTurnResult {
	results := make(chan startTurnResult, 1)
	go func() {
		handle, err := s.StartTurn(ctx, turnRequest(model))
		results <- startTurnResult{handle: handle, err: err}
	}()
	return results
}

func waitForReplacementReady(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("replacement ready channel did not close")
	}
}

func assertTransportWrite(t *testing.T, next *scriptedTransport, want string) {
	t.Helper()
	select {
	case write := <-next.stdin.writes:
		if !strings.Contains(write, want) {
			t.Fatalf("transport write = %q, want turn payload", write)
		}
	case <-time.After(time.Second):
		t.Fatal("StartTurn did not write after transport recovery")
	}
}

func assertStartTurnSuccess(t *testing.T, results <-chan startTurnResult) {
	t.Helper()
	select {
	case res := <-results:
		if res.err != nil || res.handle == nil {
			t.Fatalf("StartTurn result = handle:%v err:%v, want success", res.handle, res.err)
		}
	case <-time.After(time.Second):
		t.Fatal("StartTurn did not finish after transport recovery")
	}
}

func assertResumeID(t *testing.T, resumeIDs <-chan string, want string) {
	t.Helper()
	select {
	case resumeID := <-resumeIDs:
		if resumeID != want {
			t.Fatalf("resumeID = %q, want UUID", resumeID)
		}
	default:
		t.Fatal("restart launch was not invoked")
	}
}
