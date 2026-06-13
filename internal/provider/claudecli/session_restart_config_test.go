package claudecli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestRestartIfNeededLockedCommitsPendingConfigAfterReady(t *testing.T) {
	next := newScriptedTransport()
	defer next.finish()
	launchFn := overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		return next.tr, nil, nil
	})

	pendingModel := "sonnet[1m]"
	pendingEffort := "max"
	oldReady := make(chan struct{})
	close(oldReady)
	s := newPendingConfigRestartSession(oldReady, &pendingModel, &pendingEffort, launchFn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := restartLockedAsync(s, ctx)

	_ = waitForReadySwap(t, s, oldReady)
	assertPendingConfigWaiting(t, s, pendingModel, pendingEffort)

	next.emitSystemInit(t, "11111111-2222-3333-4444-555555555555")
	waitRestartResult(t, result, "did not return after ready")
	assertPendingConfigCommitted(t, s, pendingModel)
	assertPendingConfigReadback(t, s, pendingModel, pendingEffort)
}

func newPendingConfigRestartSession(oldReady chan struct{}, pendingModel, pendingEffort *string, launchFn testLaunchCLI) *session {
	return &session{
		threadID:        "pending",
		sessionID:       "pending",
		threadReady:     oldReady,
		transport:       closedTransport(),
		model:           "opus",
		transportModel:  "opus",
		config:          cliLaunchConfig{Effort: "high"},
		pendingModel:    pendingModel,
		pendingEffort:   pendingEffort,
		configDirty:     true,
		launchCLI:       launchFn,
		suppressedTurns: map[string]struct{}{},
	}
}

func restartLockedAsync(s *session, ctx context.Context) <-chan error {
	result := make(chan error, 1)
	go func() {
		s.mu.Lock()
		err := s.restartIfNeededLocked(ctx, dto.TurnRequest{})
		s.mu.Unlock()
		result <- err
	}()
	return result
}

func assertPendingConfigWaiting(t *testing.T, s *session, pendingModel, pendingEffort string) {
	t.Helper()
	if s.model != "opus" || s.config.Effort != "high" {
		t.Fatalf("live state mutated before ready: model=%q effort=%q", s.model, s.config.Effort)
	}
	if s.pendingModel == nil || *s.pendingModel != pendingModel || s.pendingEffort == nil || *s.pendingEffort != pendingEffort {
		t.Fatalf("pending state lost before ready: %#v", s)
	}
}

func waitRestartResult(t *testing.T, result <-chan error, timeoutMessage string) {
	t.Helper()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("restartIfNeededLocked() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal(timeoutMessage)
	}
}

func assertPendingConfigCommitted(t *testing.T, s *session, pendingModel string) {
	t.Helper()
	if s.model != pendingModel || s.config.Effort != "high" {
		t.Fatalf("live state after commit = model:%q effort:%q, want %q/high", s.model, s.config.Effort, pendingModel)
	}
	if s.pendingModel != nil || s.pendingEffort != nil || s.configDirty {
		t.Fatalf("pending state not cleared after commit: %#v", s)
	}
}

func assertPendingConfigReadback(t *testing.T, s *session, pendingModel, pendingEffort string) {
	t.Helper()
	cfg, err := s.ReadConfig(context.Background(), "")
	if err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}
	if cfg.Override.Model != pendingModel || cfg.Override.Effort != pendingEffort {
		t.Fatalf("Override = %#v, want %q/%q", cfg.Override, pendingModel, pendingEffort)
	}
	if cfg.Effective.Model != pendingModel || cfg.Effective.Effort != "high" {
		t.Fatalf("Effective = %#v, want %q/high", cfg.Effective, pendingModel)
	}
}

func TestRestartIfNeededLockedConsumesCanonicalNoopPendingEffort(t *testing.T) {
	tr := &transport{stdin: &recordingWriteCloser{}, done: make(chan struct{})}
	oldReady := make(chan struct{})
	close(oldReady)
	pendingEffort := "max"
	s := &session{
		threadID:          "thread-1",
		sessionID:         "thread-1",
		threadReady:       oldReady,
		transport:         tr,
		model:             "sonnet",
		transportModel:    "sonnet",
		config:            cliLaunchConfig{Effort: "high"},
		overrideEffort:    pendingEffort,
		overrideEffortSet: true,
		pendingEffort:     &pendingEffort,
		configDirty:       true,
		suppressedTurns:   map[string]struct{}{},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	s.mu.Lock()
	err := s.restartIfNeededLocked(ctx, dto.TurnRequest{})
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("restartIfNeededLocked() error = %v", err)
	}
	if s.pendingEffort != nil || s.configDirty {
		t.Fatalf("pending effort not consumed after canonical no-op: %#v", s)
	}
	cfg, err := s.ReadConfig(context.Background(), "")
	if err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}
	if cfg.Override.Effort != "max" || cfg.Effective.Effort != "high" {
		t.Fatalf("ReadConfig() = %#v, want override=max effective=high", cfg)
	}
}

func TestRestartIfNeededLockedPreservesPendingConfigOnWaitError(t *testing.T) {
	next := newScriptedTransport()
	defer next.finish()
	launchFn := overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		return next.tr, nil, nil
	})

	pendingModel := "sonnet"
	pendingEffort := "high"
	old := newBufferedTransport(t, "thread-1")
	oldReady := make(chan struct{})
	close(oldReady)
	s := &session{
		threadID:        "pending",
		sessionID:       "pending",
		threadReady:     oldReady,
		transport:       old.tr,
		model:           "opus",
		transportModel:  "opus",
		config:          cliLaunchConfig{Effort: "low"},
		pendingModel:    &pendingModel,
		pendingEffort:   &pendingEffort,
		configDirty:     true,
		launchCLI:       launchFn,
		suppressedTurns: map[string]struct{}{},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	s.mu.Lock()
	err := s.restartIfNeededLocked(ctx, dto.TurnRequest{})
	s.mu.Unlock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("restartIfNeededLocked() error = %v, want context deadline exceeded", err)
	}
	if s.transport != old.tr {
		t.Fatal("old transport was not restored after restart wait failure")
	}
	if s.model != "opus" || s.config.Effort != "low" {
		t.Fatalf("live state changed on failed restart: model=%q effort=%q", s.model, s.config.Effort)
	}
	if s.pendingModel == nil || *s.pendingModel != pendingModel || s.pendingEffort == nil || *s.pendingEffort != pendingEffort || !s.configDirty {
		t.Fatalf("pending state lost on failed restart: %#v", s)
	}
}

func TestRestartIfNeededLockedUsesPromptSnapshot(t *testing.T) {
	next := newScriptedTransport()
	defer next.finish()
	launched := make(chan struct{}, 1)
	var launchedInstructions string
	var launchedConfig cliLaunchConfig
	launchFn := overrideLaunchCLI(t, func(_, _, _, instructions string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		launchedInstructions = instructions
		launchedConfig = cfg
		launched <- struct{}{}
		return next.tr, nil, nil
	})

	oldReady := make(chan struct{})
	close(oldReady)
	snapshot := contract.PromptAssemblySnapshot{
		BaseInstructions:      "assembled base",
		DeveloperInstructions: "assembled developer",
	}
	s := &session{
		threadID:        "pending",
		sessionID:       "pending",
		threadReady:     oldReady,
		transport:       closedTransport(),
		model:           "sonnet",
		transportModel:  "sonnet",
		instructions:    "legacy base",
		config:          cliLaunchConfig{PromptSnapshot: snapshot},
		transportConfig: cliLaunchConfig{PromptSnapshot: snapshot},
		launchCLI:       launchFn,
		suppressedTurns: map[string]struct{}{},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		s.mu.Lock()
		err := s.restartIfNeededLocked(ctx, dto.TurnRequest{})
		s.mu.Unlock()
		result <- err
	}()

	select {
	case <-launched:
		if launchedInstructions != "assembled base" {
			t.Fatalf("launch instructions = %q, want assembled base", launchedInstructions)
		}
		if launchedConfig.PromptSnapshot.DeveloperInstructions != "assembled developer" {
			t.Fatalf("launch config = %#v", launchedConfig)
		}
	case <-time.After(time.Second):
		t.Fatal("restart did not launch replacement transport")
	}

	next.emitSystemInit(t, "11111111-2222-3333-4444-555555555555")
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("restartIfNeededLocked() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("restartIfNeededLocked() did not finish")
	}
}
