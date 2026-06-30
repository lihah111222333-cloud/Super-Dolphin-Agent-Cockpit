package claudecli

import (
	"context"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/kelindar/event"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

func TestRestartIfNeededLockedPublishesRestartStatusPatch(t *testing.T) {
	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	patches := make(chan uidto.UIThreadPatch, 4)
	cancel := event.Subscribe(bus, func(ev uidto.UIThreadPatch) {
		if ev.Source == "claude/restart" {
			patches <- ev
		}
	})
	defer cancel()

	next := newScriptedTransport()
	defer next.finish()
	launchFn := overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		return next.tr, nil, nil
	})

	oldReady := make(chan struct{})
	close(oldReady)
	s := &session{
		agentID:         "agent-1",
		threadID:        "pending",
		publicThreadID:  "thread-public",
		sessionID:       "pending",
		threadReady:     oldReady,
		transport:       closedTransport(),
		model:           "sonnet",
		transportModel:  "sonnet",
		config:          cliLaunchConfig{Effort: "high", PromptSnapshot: validResumePromptSnapshotForTest()},
		eventDispatcher: dispatcher,
		launchCLI:       launchFn,
		suppressedTurns: map[string]struct{}{},
	}
	ctx, cancelCtx := context.WithTimeout(context.Background(), time.Second)
	defer cancelCtx()
	result := restartLockedAsync(s, ctx)

	select {
	case patch := <-patches:
		assertRestartStatusPatch(t, patch)
	case <-time.After(time.Second):
		t.Fatal("restart status patch was not published")
	}

	next.emitSystemInit(t, "11111111-2222-3333-4444-555555555555")
	waitRestartResult(t, result, "restartIfNeededLocked() did not finish")
}

func assertRestartStatusPatch(t *testing.T, patch uidto.UIThreadPatch) {
	t.Helper()
	if patch.ThreadID != "thread-public" {
		t.Fatalf("status patch threadID = %q, want thread-public", patch.ThreadID)
	}
	if patch.Status != "syncing" {
		t.Fatalf("status patch status = %q, want syncing", patch.Status)
	}
	if patch.StatusHeader != "Claude 重启中…" {
		t.Fatalf("status patch header = %q, want Claude 重启中…", patch.StatusHeader)
	}
	if patch.StatusDetails == "" {
		t.Fatal("status patch details = empty, want restart details")
	}
}

func TestDriverStartCanonicalizesEffectiveEffort(t *testing.T) {
	next := newBufferedTransport(t, "thread-1")
	var launchedInstructions string
	var launchedConfig cliLaunchConfig
	launchFn := overrideLaunchCLI(t, func(_, _, _, instructions string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		launchedInstructions = instructions
		launchedConfig = cfg
		return next.tr, nil, nil
	})

	d := &driver{
		mirror:     &recordingMirrorReconciler{},
		launchCLI:  launchFn,
		authStatus: loggedInClaudeAuthStatus,
	}
	sess, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider:     "claude",
		AgentID:      "agent-1",
		CWD:          t.TempDir(),
		Model:        "sonnet",
		Instructions: "legacy base",
		Config: map[string]any{
			"effort":                "max",
			"developerInstructions": "legacy developer",
		},
		StartAssembly: contract.StartAssembly{
			BaseInstructions:      "assembled base",
			DeveloperInstructions: "assembled developer",
			Snapshot: contract.PromptAssemblySnapshot{
				BaseInstructions:      "assembled base",
				DeveloperInstructions: "assembled developer",
			},
		},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s, ok := sess.(*session)
	if !ok {
		t.Fatalf("StartSession() type = %T, want *session", sess)
	}
	assertCanonicalEffortConfig(t, s)
	assertCanonicalEffortLaunch(t, launchedInstructions, launchedConfig)
	assertCanonicalRuntimeConfig(t, s)
}

func assertCanonicalEffortConfig(t *testing.T, s *session) {
	t.Helper()
	cfg, err := s.ReadConfig(context.Background(), "")
	if err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}
	if cfg.Override.Effort != "" {
		t.Fatalf("Override = %#v, want no synthetic override", cfg.Override)
	}
	if cfg.Effective.Model != "sonnet" || cfg.Effective.Effort != "high" {
		t.Fatalf("Effective = %#v, want sonnet/high", cfg.Effective)
	}
}

func assertCanonicalEffortLaunch(t *testing.T, launchedInstructions string, launchedConfig cliLaunchConfig) {
	t.Helper()
	if launchedInstructions != "assembled base" {
		t.Fatalf("launch instructions = %q, want assembled base", launchedInstructions)
	}
	if launchedConfig.PromptSnapshot.BaseInstructions != "assembled base" ||
		launchedConfig.PromptSnapshot.DeveloperInstructions != "assembled developer" {
		t.Fatalf("launch prompt snapshot = %#v", launchedConfig.PromptSnapshot)
	}
}

func assertCanonicalRuntimeConfig(t *testing.T, s *session) {
	t.Helper()
	runtimeCfg := s.RuntimeConfigSnapshot()
	if runtimeCfg["baseInstructions"] != "assembled base" ||
		runtimeCfg["developerInstructions"] != "assembled developer" {
		t.Fatalf("RuntimeConfigSnapshot() = %#v", runtimeCfg)
	}
}
