package claudecli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
)

type failingWriteCloser struct {
	writes chan string
	err    error
}

func (f *failingWriteCloser) Write(p []byte) (int, error) {
	if f.writes != nil {
		f.writes <- string(append([]byte(nil), p...))
	}
	return len(p), f.err
}

func (f *failingWriteCloser) Close() error { return nil }

func TestStartTurnRestartsUnavailableTransportWithoutSettingsChange(t *testing.T) {
	next := newScriptedTransport()
	defer next.finish()
	resumeIDs := make(chan string, 1)
	launchFn := overrideLaunchCLI(t, func(_, _, _, _ string, _ cliLaunchConfig, _ dto.MCPManifest, resumeID string) (*transport, func(), error) {
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
		launchCLI:       launchFn,
		suppressedTurns: map[string]struct{}{},
		model:           "claude-same",
		config:          cliLaunchConfig{PromptSnapshot: validResumePromptSnapshotForTest()},
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results := startTurnAsync(t, ctx, s, "claude-same")

	ready := waitForReadySwap(t, s, oldReady)
	next.emitSystemInit(t, "11111111-2222-3333-4444-555555555555")
	waitForReplacementReady(t, ready)
	assertTransportWrite(t, next, "hello")
	assertStartTurnSuccess(t, results)
	assertResumeID(t, resumeIDs, "11111111-2222-3333-4444-555555555555")
}

func TestStartTurnWriteFailureRestartsTransportWithoutReplayingFailedTurn(t *testing.T) {
	next := newScriptedTransport()
	defer next.finish()
	resumeIDs := make(chan string, 1)
	launchFn := overrideLaunchCLI(t, func(_, _, _, _ string, _ cliLaunchConfig, _ dto.MCPManifest, resumeID string) (*transport, func(), error) {
		resumeIDs <- resumeID
		return next.tr, nil, nil
	})

	oldReady := make(chan struct{})
	close(oldReady)
	wantErr := errors.New("stdin write failed")
	failingStdin := &failingWriteCloser{writes: make(chan string, 1), err: wantErr}
	s := assumeSessionLaunchOverride(&session{
		threadID:        "11111111-2222-3333-4444-555555555555",
		sessionID:       "11111111-2222-3333-4444-555555555555",
		threadReady:     oldReady,
		transport:       &transport{stdin: failingStdin, stderr: newLimitedBuffer(stderrLimitBytes), done: make(chan struct{})},
		launchCLI:       launchFn,
		suppressedTurns: map[string]struct{}{},
		model:           "claude-same",
		config:          cliLaunchConfig{PromptSnapshot: validResumePromptSnapshotForTest()},
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	handle, err := s.StartTurn(ctx, recoveryTurnRequest("claude-same", "first-message"))
	if !errors.Is(err, wantErr) || handle != nil {
		t.Fatalf("StartTurn result = handle:%v err:%v, want write failure", handle, err)
	}
	assertFailingTransportWrite(t, failingStdin, "first-message")

	ready := waitForWriteFailureRecoverySwap(t, s, oldReady)
	waitForReplacementReady(t, ready)
	assertResumeID(t, resumeIDs, "11111111-2222-3333-4444-555555555555")
	assertNoReplacementWrite(t, next, "failed turn was replayed")

	followupCtx, followupCancel := context.WithTimeout(context.Background(), time.Second)
	defer followupCancel()
	handle, err = s.StartTurn(followupCtx, recoveryTurnRequest("claude-same", "second-message"))
	if err != nil || handle == nil {
		t.Fatalf("follow-up StartTurn result = handle:%v err:%v, want success", handle, err)
	}
	assertTransportWrite(t, next, "second-message")
}

func startTurnAsync(t testing.TB, ctx context.Context, s *session, model string) <-chan startTurnResult {
	t.Helper()
	results := make(chan startTurnResult, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		handle, err := s.StartTurn(ctx, turnRequest(model))
		results <- startTurnResult{handle: handle, err: err}
	})
	return results
}

func recoveryTurnRequest(model, content string) dto.TurnRequest {
	return dto.TurnRequest{
		Inputs: []shareddto.InputItem{{Type: "text", Content: content}},
		Overrides: dto.TurnOverrides{
			Model: model,
		},
	}
}

func waitForWriteFailureRecoverySwap(t *testing.T, s *session, oldReady chan struct{}) chan struct{} {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, _, ready := snapshotSessionState(s)
		if ready != nil && ready != oldReady {
			return ready
		}
		select {
		case <-deadline:
			t.Fatal("write failure did not trigger transport recovery")
		case <-ticker.C:
		}
	}
}

func waitForReplacementReady(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("replacement ready channel did not close")
	}
}

func assertFailingTransportWrite(t *testing.T, stdin *failingWriteCloser, want string) {
	t.Helper()
	select {
	case write := <-stdin.writes:
		if !strings.Contains(write, want) {
			t.Fatalf("failed transport write = %q, want %q", write, want)
		}
	case <-time.After(time.Second):
		t.Fatal("failed transport did not receive original payload")
	}
}

func assertNoReplacementWrite(t *testing.T, next *scriptedTransport, reason string) {
	t.Helper()
	select {
	case write := <-next.stdin.writes:
		t.Fatalf("replacement transport received unexpected write %q: %s", write, reason)
	case <-time.After(100 * time.Millisecond):
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
