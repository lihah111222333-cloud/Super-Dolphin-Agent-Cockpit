package claudecli

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/kelindar/event"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

func TestForceStopIncludesStderrInFailedEvent(t *testing.T) {
	t.Parallel()

	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	got := make(chan dto.BusRawProviderEvent, 1)
	cancel := event.Subscribe(bus, func(ev dto.BusRawProviderEvent) {
		if ev.Event.EventType == "agent:failed" {
			got <- ev
		}
	})
	defer cancel()

	stderrBuf := newLimitedBuffer(1024)
	_, _ = stderrBuf.Write([]byte("Error: authentication failed\n"))

	// done already closed so Kill() won't block; cmd nil so signal is a no-op.
	done := make(chan struct{})
	close(done)

	s := &session{
		agentID:         "agent-1",
		threadID:        "thread-1",
		sessionID:       "thread-1",
		transport:       &transport{stderr: stderrBuf, done: done},
		eventDispatcher: dispatcher,
	}

	_ = s.stop(true)

	select {
	case ev := <-got:
		data, _ := ev.Event.Data.(map[string]any)
		stderr, _ := data["stderr"].(string)
		if stderr != "Error: authentication failed\n" {
			t.Fatalf("agent:failed stderr = %q, want stderr content from transport", stderr)
		}
	case <-time.After(time.Second):
		t.Fatal("agent:failed event was not published")
	}
}

func TestBaseDataUsesPublicThreadIDAndSeparateSessionID(t *testing.T) {
	t.Parallel()

	got := baseData(rawBase{
		AgentID:  "agent-1",
		ThreadID: "thread-public",
		TurnID:   "turn-1",
	}, "session-123", "2026-03-26T00:00:00Z")

	if got["thread_id"] != "thread-public" {
		t.Fatalf("thread_id = %v, want thread-public", got["thread_id"])
	}
	if got["session_id"] != "session-123" {
		t.Fatalf("session_id = %v, want session-123", got["session_id"])
	}
}

func TestHandleReceiveExitEOFCompletesActiveTurn(t *testing.T) {
	t.Parallel()

	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	got := make(chan turndto.TurnCompleted, 1)
	cancel := event.Subscribe(bus, func(ev turndto.TurnCompleted) {
		got <- ev
	})
	defer cancel()

	tr := &transport{}
	handle := newTurnHandle("local-1", "turn-1")
	s := &session{
		agentID:         "agent-1",
		threadID:        "thread-1",
		sessionID:       "thread-1",
		transport:       tr,
		activeTurn:      handle,
		eventDispatcher: dispatcher,
	}

	s.handleReceiveExit(tr, io.EOF)

	select {
	case <-handle.Done():
	case <-time.After(time.Second):
		t.Fatal("handle was not completed on EOF")
	}
	if !errors.Is(handle.Err(), io.EOF) {
		t.Fatalf("handle.Err() = %v, want EOF", handle.Err())
	}
	if s.activeTurn != nil {
		t.Fatal("activeTurn was not cleared")
	}

	select {
	case ev := <-got:
		if ev.Success {
			t.Fatal("TurnCompleted.Success = true, want false")
		}
		if ev.Error != io.EOF.Error() {
			t.Fatalf("TurnCompleted.Error = %q, want %q", ev.Error, io.EOF.Error())
		}
		if ev.TurnID != "turn-1" {
			t.Fatalf("TurnCompleted.TurnID = %q, want turn-1", ev.TurnID)
		}
	case <-time.After(time.Second):
		t.Fatal("TurnCompleted event was not published")
	}
}

func TestDriverResumeSessionPublishesPublicThreadID(t *testing.T) {
	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	got := make(chan agentdto.AgentLaunched, 4)
	cancel := event.Subscribe(bus, func(ev agentdto.AgentLaunched) {
		got <- ev
	})
	defer cancel()

	next := newBufferedTransport(t, "provider-thread-1")
	overrideLaunchCLI(t, func(_, _, _, instructions string, cfg cliLaunchConfig, _ dto.MCPManifest, resumeID string) (*transport, func(), error) {
		if resumeID != "provider-thread-1" {
			t.Fatalf("resumeID = %q, want provider-thread-1", resumeID)
		}
		if instructions != "stored base" {
			t.Fatalf("instructions = %q, want stored base", instructions)
		}
		if cfg.PromptSnapshot.BaseInstructions != "stored base" || cfg.PromptSnapshot.DeveloperInstructions != "stored dev" {
			t.Fatalf("PromptSnapshot = %#v, want stored snapshot", cfg.PromptSnapshot)
		}
		return next.tr, nil, nil
	})

	d := &driver{eventDispatcher: dispatcher}
	resumed, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		Provider:         "claude",
		AgentID:          "agent-1",
		ThreadID:         "thread-public",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/tmp/repo",
		PromptSnapshot: dto.PromptAssemblySnapshot{
			DisplayName:           "thread-public",
			BaseInstructions:      "stored base",
			DeveloperInstructions: "stored dev",
			Provider:              "claude",
			Version:               contract.PromptAssemblySnapshotVersion,
			Hash:                  "snapshot-hash",
		},
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	s, ok := resumed.(*session)
	if !ok {
		t.Fatalf("ResumeSession() type = %T, want *session", resumed)
	}
	if s.ThreadID() != "provider-thread-1" {
		t.Fatalf("ThreadID() = %q, want provider-thread-1", s.ThreadID())
	}
	if s.EventThreadID() != "thread-public" {
		t.Fatalf("EventThreadID() = %q, want thread-public", s.EventThreadID())
	}
	for i := 0; i < 2; i++ {
		select {
		case ev := <-got:
			if ev.ThreadID != "thread-public" {
				t.Fatalf("AgentLaunched.ThreadID = %q, want thread-public", ev.ThreadID)
			}
			if ev.SessionID != "provider-thread-1" {
				t.Fatalf("AgentLaunched.SessionID = %q, want provider-thread-1", ev.SessionID)
			}
		case <-time.After(time.Second):
			t.Fatal("AgentLaunched event was not published")
		}
	}
}

func TestDriverResumeSessionRehydratesClaudeOverrideState(t *testing.T) {
	next := newBufferedTransport(t, "provider-thread-override")
	model := "claude-sonnet-4-20250514[1m]"
	effectiveEffort := "high"
	overrideEffort := "max"
	overrideLaunchCLI(t, func(_, _, passedModel, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, resumeID string) (*transport, func(), error) {
		if resumeID != "provider-thread-override" {
			t.Fatalf("resumeID = %q, want provider-thread-override", resumeID)
		}
		if passedModel != model {
			t.Fatalf("launch model = %q, want %q", passedModel, model)
		}
		if cfg.Effort != effectiveEffort {
			t.Fatalf("launch effort = %q, want %q", cfg.Effort, effectiveEffort)
		}
		return next.tr, nil, nil
	})

	d := &driver{}
	resumed, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		Provider:         "claude",
		AgentID:          "agent-1",
		ThreadID:         "thread-public",
		ProviderThreadID: "provider-thread-override",
		CWD:              "/tmp/repo",
		Model:            model,
		Effort:           effectiveEffort,
		ConfigOverride: dto.ThreadConfigPatch{
			Model:  &model,
			Effort: &overrideEffort,
		},
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	s, ok := resumed.(*session)
	if !ok {
		t.Fatalf("ResumeSession() type = %T, want *session", resumed)
	}
	if !s.overrideModelSet || s.overrideModel != model {
		t.Fatalf("override model state = (%v, %q), want true/%q", s.overrideModelSet, s.overrideModel, model)
	}
	if !s.overrideEffortSet || s.overrideEffort != overrideEffort {
		t.Fatalf("override effort state = (%v, %q), want true/%q", s.overrideEffortSet, s.overrideEffort, overrideEffort)
	}
	cfg, err := s.ReadConfig(context.Background(), "")
	if err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}
	if cfg.Override.Model != model || cfg.Override.Effort != overrideEffort {
		t.Fatalf("Override = %#v, want model=%q effort=%q", cfg.Override, model, overrideEffort)
	}
	if cfg.Effective.Model != model || cfg.Effective.Effort != effectiveEffort {
		t.Fatalf("Effective = %#v, want model=%q effort=%q", cfg.Effective, model, effectiveEffort)
	}
}

func TestDriverResumeSessionPreservesExplicitClearOverrideState(t *testing.T) {
	next := newBufferedTransport(t, "provider-thread-clear")
	empty := ""
	effectiveModel := "sonnet"
	effectiveEffort := "high"
	overrideLaunchCLI(t, func(_, _, passedModel, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, resumeID string) (*transport, func(), error) {
		if resumeID != "provider-thread-clear" {
			t.Fatalf("resumeID = %q, want provider-thread-clear", resumeID)
		}
		if passedModel != effectiveModel {
			t.Fatalf("launch model = %q, want %q", passedModel, effectiveModel)
		}
		if cfg.Effort != effectiveEffort {
			t.Fatalf("launch effort = %q, want %q", cfg.Effort, effectiveEffort)
		}
		return next.tr, nil, nil
	})

	d := &driver{}
	resumed, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		Provider:         "claude",
		AgentID:          "agent-1",
		ThreadID:         "thread-public",
		ProviderThreadID: "provider-thread-clear",
		CWD:              "/tmp/repo",
		Model:            effectiveModel,
		Effort:           effectiveEffort,
		ConfigOverride: dto.ThreadConfigPatch{
			Model:  &empty,
			Effort: &empty,
		},
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	s, ok := resumed.(*session)
	if !ok {
		t.Fatalf("ResumeSession() type = %T, want *session", resumed)
	}
	if !s.overrideModelSet || s.overrideModel != "" {
		t.Fatalf("override model clear state = (%v, %q), want true/empty", s.overrideModelSet, s.overrideModel)
	}
	if !s.overrideEffortSet || s.overrideEffort != "" {
		t.Fatalf("override effort clear state = (%v, %q), want true/empty", s.overrideEffortSet, s.overrideEffort)
	}
	cfg, err := s.ReadConfig(context.Background(), "")
	if err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}
	if cfg.Override.Model != "" || cfg.Override.Effort != "" {
		t.Fatalf("Override = %#v, want explicit clear reflected as empty override", cfg.Override)
	}
	if cfg.Effective.Model != effectiveModel || cfg.Effective.Effort != effectiveEffort {
		t.Fatalf("Effective = %#v, want %q/%q", cfg.Effective, effectiveModel, effectiveEffort)
	}
}
