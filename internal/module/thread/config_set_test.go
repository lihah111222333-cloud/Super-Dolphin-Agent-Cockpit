package thread

import (
	"context"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	"github.com/kelindar/event"
)

func TestSetConfigConfiguresModelAndEffort(t *testing.T) {
	t.Parallel()

	model := "gpt-5.5"
	effort := "high"
	session := &stubSession{
		threadID:      "thread-1",
		allowedModels: []string{"gpt-5.5"},
		readConfigResult: dto.ThreadConfig{
			ThreadID: "thread-1",
			Provider: "codex",
			Effective: dto.ThreadConfigValues{
				Model:  "gpt-5.5",
				Effort: "high",
			},
		},
	}
	sessions := &stubSessionProvider{session: session}
	threads := newConfigPersistenceThreadStore()
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "thread-1",
		CodexThreadID:    "thread-1",
	}}
	svc := NewService(silentLogger(), threads, bindings, sessions, nil, nil, nil, nil)

	cfg, err := svc.SetConfig(context.Background(), "thread-1", dto.ThreadConfigPatch{
		Model:  &model,
		Effort: &effort,
	})
	if err != nil {
		t.Fatalf("SetConfig() error = %v", err)
	}
	if session.configureCalls != 1 {
		t.Fatalf("configureCalls = %d, want 1", session.configureCalls)
	}
	if session.configurePatch.Model == nil || *session.configurePatch.Model != model {
		t.Fatalf("configurePatch.Model = %#v, want %q", session.configurePatch.Model, model)
	}
	if session.configurePatch.Effort == nil || *session.configurePatch.Effort != effort {
		t.Fatalf("configurePatch.Effort = %#v, want %q", session.configurePatch.Effort, effort)
	}
	if cfg.Effective.Model != model || cfg.Effective.Effort != effort {
		t.Fatalf("SetConfig() = %#v", cfg)
	}
}

func TestSetConfigInvalidatesPromptAssemblyForSetupFlip(t *testing.T) {
	t.Parallel()

	model := "gpt-5.5"
	effort := "high"
	promptAssembly := &stubPromptAssemblyService{}
	session := &stubSession{
		threadID:      "thread-1",
		allowedModels: []string{"gpt-5.5"},
		readConfigResult: dto.ThreadConfig{
			ThreadID: "thread-1",
			Provider: "codex",
			Effective: dto.ThreadConfigValues{
				Model:  "gpt-5.5",
				Effort: "high",
			},
		},
	}
	threads := newConfigPersistenceThreadStore()
	svc := NewServiceWithPromptAssembly(
		silentLogger(),
		threads,
		&stubBindingStore{binding: &bindingstore.Binding{AgentID: "agent-1", Provider: "codex", ProviderThreadID: "thread-1", CodexThreadID: "thread-1"}},
		&stubSessionProvider{session: session},
		nil,
		nil,
		nil,
		nil,
		promptAssembly,
		nil,
		nil,
	).(*service)

	if _, err := svc.SetConfig(context.Background(), "thread-1", dto.ThreadConfigPatch{Model: &model, Effort: &effort}); err != nil {
		t.Fatalf("SetConfig() error = %v", err)
	}
	if got := promptAssembly.invalidated; len(got) != 1 || got[0] != contract.InvalidateProviderSwitch {
		t.Fatalf("Invalidate calls = %#v, want [%q]", got, contract.InvalidateProviderSwitch)
	}
}

func TestSetModelInvalidatesPromptAssemblyForSetupFlip(t *testing.T) {
	t.Parallel()

	promptAssembly := &stubPromptAssemblyService{}
	session := &stubSession{
		threadID:      "thread-1",
		allowedModels: []string{"gpt-5.5"},
		readConfigResult: dto.ThreadConfig{
			ThreadID:  "thread-1",
			Provider:  "codex",
			Effective: dto.ThreadConfigValues{Model: "gpt-5.5"},
		},
	}
	threads := newConfigPersistenceThreadStore()
	svc := NewServiceWithPromptAssembly(
		silentLogger(),
		threads,
		&stubBindingStore{binding: &bindingstore.Binding{AgentID: "agent-1", Provider: "codex", ProviderThreadID: "thread-1", CodexThreadID: "thread-1"}},
		&stubSessionProvider{session: session},
		nil,
		nil,
		nil,
		nil,
		promptAssembly,
		nil,
		nil,
	).(*service)

	if _, err := svc.SetModel(context.Background(), "thread-1", "gpt-5.5"); err != nil {
		t.Fatalf("SetModel() error = %v", err)
	}
	if got := promptAssembly.invalidated; len(got) != 1 || got[0] != contract.InvalidateProviderSwitch {
		t.Fatalf("Invalidate calls = %#v, want [%q]", got, contract.InvalidateProviderSwitch)
	}
}

func TestSetConfigRejectsInvalidEffort(t *testing.T) {
	t.Parallel()

	effort := "turbo"
	session := &stubSession{threadID: "thread-1"}
	sessions := &stubSessionProvider{session: session}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "thread-1",
		CodexThreadID:    "thread-1",
	}}
	svc := NewService(silentLogger(), nil, bindings, sessions, nil, nil, nil, nil)

	if _, err := svc.SetConfig(context.Background(), "thread-1", dto.ThreadConfigPatch{Effort: &effort}); err == nil {
		t.Fatal("SetConfig() error = nil, want invalid effort")
	}
	if session.configureCalls != 0 {
		t.Fatalf("configureCalls = %d, want 0", session.configureCalls)
	}
}

func TestSetConfigReturnsOverrideWithoutMutatingProviderEffectiveConfigAndPublishesUpdated(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()
	updates := make(chan threaddto.Updated, 1)
	cancel := event.Subscribe(dispatcher, func(ev threaddto.Updated) { updates <- ev })
	defer cancel()

	model := "gpt-5.5"
	threads := &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-1", Model: "o4-mini", Status: statusCreated, CreatedAt: 1, UpdatedAt: 1}}
	session := &stubSession{
		threadID:      "thread-1",
		allowedModels: []string{model},
		readConfigResult: dto.ThreadConfig{
			ThreadID:               "thread-1",
			Provider:               "codex",
			SupportsThreadOverride: true,
			Override:               dto.ThreadConfigValues{Model: "o4-mini"},
			Effective:              dto.ThreadConfigValues{Model: "o4-mini", Effort: "medium"},
		},
	}
	svc := NewService(
		silentLogger(),
		threads,
		&stubBindingStore{binding: &bindingstore.Binding{AgentID: "agent-1", Provider: "codex", ProviderThreadID: "thread-1", CodexThreadID: "thread-1"}},
		&stubSessionProvider{session: session},
		nil,
		nil,
		nil,
		bus.NewThreadEmitters(dispatcher),
	)

	cfg, err := svc.SetConfig(context.Background(), "thread-1", dto.ThreadConfigPatch{Model: &model})
	if err != nil {
		t.Fatalf("SetConfig() error = %v", err)
	}
	if cfg.Override.Model != model {
		t.Fatalf("Override.Model = %q, want %q", cfg.Override.Model, model)
	}
	if cfg.Effective.Model != "o4-mini" || cfg.Effective.Effort != "medium" {
		t.Fatalf("Effective = %#v, want provider live values", cfg.Effective)
	}
	if threads.thread.Model != model {
		t.Fatalf("stored model = %q, want %q", threads.thread.Model, model)
	}
	assertThreadModelUpdatedEvent(t, updates, model)
}

func assertThreadModelUpdatedEvent(t *testing.T, updates <-chan threaddto.Updated, model string) {
	t.Helper()
	select {
	case ev := <-updates:
		if ev.ThreadID != "thread-1" || ev.Model == nil || *ev.Model != model {
			t.Fatalf("updated event = %#v, want model %q", ev, model)
		}
	case <-time.After(time.Second):
		t.Fatal("expected threaddto.Updated event")
	}
}

func TestSetConfigClearsModelOverrideWithoutClearingProviderEffectiveModelAndPublishesUpdated(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()
	updates := make(chan threaddto.Updated, 1)
	cancel := event.Subscribe(dispatcher, func(ev threaddto.Updated) { updates <- ev })
	defer cancel()

	model := ""
	threads := &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-1", Model: "o4-mini", Status: statusCreated, CreatedAt: 1, UpdatedAt: 1}}
	session := &stubSession{
		threadID: "thread-1",
		readConfigResult: dto.ThreadConfig{
			ThreadID:               "thread-1",
			Provider:               "codex",
			SupportsThreadOverride: true,
			Override:               dto.ThreadConfigValues{Model: "o4-mini"},
			Effective:              dto.ThreadConfigValues{Model: "o4-mini", Effort: "medium"},
		},
	}
	svc := NewService(
		silentLogger(),
		threads,
		&stubBindingStore{binding: &bindingstore.Binding{AgentID: "agent-1", Provider: "codex", ProviderThreadID: "thread-1", CodexThreadID: "thread-1"}},
		&stubSessionProvider{session: session},
		nil,
		nil,
		nil,
		bus.NewThreadEmitters(dispatcher),
	)

	cfg, err := svc.SetConfig(context.Background(), "thread-1", dto.ThreadConfigPatch{Model: &model})
	if err != nil {
		t.Fatalf("SetConfig() error = %v", err)
	}
	if cfg.Override.Model != "" {
		t.Fatalf("Override.Model = %q, want cleared override", cfg.Override.Model)
	}
	if cfg.Effective.Model != "o4-mini" || cfg.Effective.Effort != "medium" {
		t.Fatalf("Effective = %#v, want provider live values", cfg.Effective)
	}
	select {
	case ev := <-updates:
		if ev.ThreadID != "thread-1" || ev.Model == nil || *ev.Model != "" {
			t.Fatalf("updated event = %#v, want empty model pointer", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("expected threaddto.Updated event")
	}
}

func TestServiceReadRuntimeConfigUsesSessionSnapshot(t *testing.T) {
	t.Parallel()

	session := &stubSession{
		threadID: "thread-1",
		runtimeConfig: map[string]any{
			"modelProvider":         "openai",
			"developerInstructions": "be precise",
			"sandbox": map[string]any{
				"type": "workspace-write",
			},
			"toolRouting": map[string]any{
				"mode":                "dynamic",
				"routerProvider":      "router-x",
				"confidenceThreshold": 0.9,
				"timeoutSec":          11,
			},
		},
	}
	sessions := &stubSessionProvider{session: session}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "thread-1",
		CodexThreadID:    "thread-1",
	}}
	svc, ok := NewService(silentLogger(), nil, bindings, sessions, nil, nil, nil, nil).(*service)
	if !ok {
		t.Fatal("NewService() type assertion failed")
	}

	got, err := svc.ReadRuntimeConfig(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("ReadRuntimeConfig() error = %v", err)
	}
	if got["modelProvider"] != "openai" || got["developerInstructions"] != "be precise" {
		t.Fatalf("ReadRuntimeConfig() = %#v", got)
	}
	sandbox, _ := got["sandbox"].(map[string]any)
	if sandbox["type"] != "workspace-write" {
		t.Fatalf("sandbox = %#v, want workspace-write", sandbox)
	}
	toolRouting, _ := got["toolRouting"].(map[string]any)
	if toolRouting["mode"] != "dynamic" || toolRouting["routerProvider"] != "router-x" {
		t.Fatalf("toolRouting = %#v", toolRouting)
	}
}

func TestSetConfigClaudeAllowsFullModelAndMaxWithoutMutatingProviderEffectiveConfig(t *testing.T) {
	t.Parallel()

	model := "claude-sonnet-4-20250514[1m]"
	effort := "max"
	threads := &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-1", Model: "sonnet", Status: statusCreated, CreatedAt: 1, UpdatedAt: 1}}
	session := &stubSession{
		threadID:      "thread-1",
		allowedModels: []string{"best", "sonnet", "sonnet[1m]", "haiku", "opus", "opus[1m]"},
		readConfigResult: dto.ThreadConfig{
			ThreadID:               "thread-1",
			Provider:               "claude",
			SupportsThreadOverride: true,
			Effective:              dto.ThreadConfigValues{Model: "sonnet", Effort: "high"},
		},
	}
	svc := NewService(
		silentLogger(),
		threads,
		&stubBindingStore{binding: &bindingstore.Binding{AgentID: "agent-1", Provider: "claude", ProviderThreadID: "thread-1"}},
		&stubSessionProvider{session: session},
		nil,
		nil,
		nil,
		nil,
	)

	cfg, err := svc.SetConfig(context.Background(), "thread-1", dto.ThreadConfigPatch{Model: &model, Effort: &effort})
	if err != nil {
		t.Fatalf("SetConfig() error = %v", err)
	}
	assertClaudeThreadConfigPatch(t, session, cfg, model, effort)
	if threads.thread.Model != model {
		t.Fatalf("stored model = %q, want %q", threads.thread.Model, model)
	}
}

func assertClaudeThreadConfigPatch(t *testing.T, session *stubSession, cfg dto.ThreadConfig, model, effort string) {
	t.Helper()
	assertSessionConfigurePatch(t, session, model, effort)
	assertClaudeThreadConfigValues(t, cfg, model, effort)
}

func assertSessionConfigurePatch(t *testing.T, session *stubSession, model, effort string) {
	t.Helper()
	if session.configureCalls != 1 {
		t.Fatalf("configureCalls = %d, want 1", session.configureCalls)
	}
	if session.configurePatch.Model == nil || *session.configurePatch.Model != model {
		t.Fatalf("configurePatch.Model = %#v, want %q", session.configurePatch.Model, model)
	}
	if session.configurePatch.Effort == nil || *session.configurePatch.Effort != effort {
		t.Fatalf("configurePatch.Effort = %#v, want %q", session.configurePatch.Effort, effort)
	}
}

func assertClaudeThreadConfigValues(t *testing.T, cfg dto.ThreadConfig, model, effort string) {
	t.Helper()
	if !cfg.SupportsThreadOverride {
		t.Fatalf("SupportsThreadOverride = false, want true: %#v", cfg)
	}
	if cfg.Override.Model != model || cfg.Override.Effort != effort {
		t.Fatalf("Override = %#v, want desired override values", cfg.Override)
	}
	if cfg.Effective.Model != "sonnet" || cfg.Effective.Effort != "high" {
		t.Fatalf("Effective = %#v, want provider live values", cfg.Effective)
	}
}

func TestSetConfigRejectsMaxForCodex(t *testing.T) {
	t.Parallel()

	model := "gpt-5.5"
	effort := "max"
	session := &stubSession{threadID: "thread-1", allowedModels: []string{model}}
	svc := NewService(
		silentLogger(),
		nil,
		&stubBindingStore{binding: &bindingstore.Binding{AgentID: "agent-1", Provider: "codex", ProviderThreadID: "thread-1", CodexThreadID: "thread-1"}},
		&stubSessionProvider{session: session},
		nil,
		nil,
		nil,
		nil,
	)

	if _, err := svc.SetConfig(context.Background(), "thread-1", dto.ThreadConfigPatch{Model: &model, Effort: &effort}); err == nil {
		t.Fatal("SetConfig() error = nil, want invalid effort")
	}
	if session.configureCalls != 0 {
		t.Fatalf("configureCalls = %d, want 0", session.configureCalls)
	}
}

func TestSetConfigReturnPatchPreservesProviderEffectiveValues(t *testing.T) {
	t.Parallel()

	ptr := func(value string) *string { return &value }
	base := dto.ThreadConfig{
		Provider:               "claude",
		SupportsThreadOverride: true,
		Override:               dto.ThreadConfigValues{Model: "custom-override", Effort: "medium"},
		Effective:              dto.ThreadConfigValues{Model: "sonnet", Effort: "high"},
	}
	cases := []struct {
		name         string
		patch        dto.ThreadConfigPatch
		wantOverride dto.ThreadConfigValues
	}{
		{
			name:         "explicit override",
			patch:        dto.ThreadConfigPatch{Model: ptr("gpt-5.5"), Effort: ptr("high")},
			wantOverride: dto.ThreadConfigValues{Model: "gpt-5.5", Effort: "high"},
		},
		{
			name:         "explicit clear restores inherit state",
			patch:        dto.ThreadConfigPatch{Model: ptr(""), Effort: ptr("")},
			wantOverride: dto.ThreadConfigValues{},
		},
		{
			name:         "claude full model with 1m and max",
			patch:        dto.ThreadConfigPatch{Model: ptr("claude-sonnet-4-20250514[1m]"), Effort: ptr("max")},
			wantOverride: dto.ThreadConfigValues{Model: "claude-sonnet-4-20250514[1m]", Effort: "max"},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := applyThreadConfigReturnPatch(base, tt.patch)
			if cfg.Override != tt.wantOverride {
				t.Fatalf("Override = %#v, want %#v", cfg.Override, tt.wantOverride)
			}
			if cfg.Effective != base.Effective {
				t.Fatalf("Effective = %#v, want %#v", cfg.Effective, base.Effective)
			}
		})
	}
}

func TestNormalizeThreadConfigPatchClaudeAllowsFullModelAndMax(t *testing.T) {
	t.Parallel()

	model := "claude-sonnet-4-20250514[1m]"
	effort := "max"
	patch, err := normalizeThreadConfigPatch(
		context.Background(),
		&stubSession{threadID: "thread-1"},
		"claude",
		dto.ThreadConfigPatch{Model: &model, Effort: &effort},
	)
	if err != nil {
		t.Fatalf("normalizeThreadConfigPatch() error = %v", err)
	}
	if patch.Model == nil || *patch.Model != model {
		t.Fatalf("patch.Model = %#v, want %q", patch.Model, model)
	}
	if patch.Effort == nil || *patch.Effort != effort {
		t.Fatalf("patch.Effort = %#v, want %q", patch.Effort, effort)
	}
}

func TestNormalizeThreadConfigPatchCodexRejectsUnlistedModel(t *testing.T) {
	t.Parallel()

	model := "gpt-5.5-preview"
	if _, err := normalizeThreadConfigPatch(
		context.Background(),
		&stubSession{threadID: "thread-1", allowedModels: []string{"gpt-5.5"}},
		"codex",
		dto.ThreadConfigPatch{Model: &model},
	); err == nil {
		t.Fatal("normalizeThreadConfigPatch() error = nil, want unsupported model")
	}
}

func TestSupportsThreadOverride(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		provider string
		want     bool
	}{
		{name: "codex", provider: "codex", want: true},
		{name: "claude", provider: "claude", want: true},
		{name: "claude with spaces", provider: "  claude  ", want: true},
		{name: "other", provider: "openai", want: false},
		{name: "empty", provider: "", want: false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := supportsThreadOverride(tt.provider); got != tt.want {
				t.Fatalf("supportsThreadOverride(%q) = %v, want %v", tt.provider, got, tt.want)
			}
		})
	}
}
