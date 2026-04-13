package thread

import (
	"context"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestServiceGetConfigPrefersSessionValueOverOfflineOverride(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:       "thread-1",
		Model:          "stale-model",
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Effort: "low", Approvals: "never"}),
	}}
	session := &stubSession{
		threadID: "thread-1",
		readConfigResult: dto.ThreadConfig{
			ThreadID:               "thread-1",
			Provider:               "codex",
			SupportsThreadOverride: true,
			Override: dto.ThreadConfigValues{
				Model:     "gpt-5.4",
				Effort:    "high",
				Approvals: "on-request",
			},
			Effective: dto.ThreadConfigValues{
				Model:     "gpt-5.4",
				Effort:    "high",
				Approvals: "on-request",
			},
		},
	}
	svc := NewService(
		silentLogger(),
		threads,
		&stubBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "thread-1",
			CodexThreadID:    "thread-1",
		}},
		&stubSessionProvider{session: session},
		nil,
		nil,
		nil,
		nil,
	)

	cfg, err := svc.GetConfig(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	if cfg.Effective.Model != "gpt-5.4" || cfg.Override.Approvals != "on-request" {
		t.Fatalf("GetConfig() = %#v", cfg)
	}
}

func TestServiceGetConfigFallsBackToOfflineConfigWithoutSession(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:       "thread-1",
		Model:          "gpt-5.4",
		Cwd:            "/tmp/demo",
		Status:         statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Effort: "high", Approvals: "never", Personality: "balanced"}),
	}}
	svc, ok := NewService(
		silentLogger(),
		threads,
		&stubBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "thread-1",
			CodexThreadID:    "thread-1",
		}},
		&stubSessionProvider{},
		nil,
		nil,
		nil,
		nil,
	).(*service)
	if !ok {
		t.Fatal("NewService() type assertion failed")
	}

	cfg, err := svc.GetConfig(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	if !cfg.SupportsThreadOverride || cfg.Provider != "codex" {
		t.Fatalf("GetConfig() provider = %#v", cfg)
	}
	if cfg.Effective.Model != "gpt-5.4" || cfg.Override.Effort != "high" || cfg.Override.Approvals != "never" {
		t.Fatalf("GetConfig() offline = %#v", cfg)
	}

	runtimeCfg, err := svc.ReadRuntimeConfig(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("ReadRuntimeConfig() error = %v", err)
	}
	if runtimeCfg["approvalPolicy"] != "never" || runtimeCfg["personality"] != "balanced" {
		t.Fatalf("ReadRuntimeConfig() = %#v", runtimeCfg)
	}
	toolRouting, _ := runtimeCfg["toolRouting"].(map[string]any)
	if toolRouting["mode"] != "legacy" || toolRouting["routerProvider"] != "openai_compatible" {
		t.Fatalf("toolRouting fallback = %#v", toolRouting)
	}
}

func TestServiceReadRuntimeConfigMergesSessionSnapshotWithOfflineConfig(t *testing.T) {
	t.Parallel()

	session := &stubSession{
		threadID: "thread-1",
		runtimeConfig: map[string]any{
			"approvalPolicy": "on-request",
		},
	}
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:       "thread-1",
		Model:          "gpt-5.4",
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Personality: "balanced", Approvals: "never"}),
	}}
	svc, ok := NewService(
		silentLogger(),
		threads,
		&stubBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "thread-1",
			CodexThreadID:    "thread-1",
		}},
		&stubSessionProvider{session: session},
		nil,
		nil,
		nil,
		nil,
	).(*service)
	if !ok {
		t.Fatal("NewService() type assertion failed")
	}

	got, err := svc.ReadRuntimeConfig(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("ReadRuntimeConfig() error = %v", err)
	}
	if got["approvalPolicy"] != "on-request" || got["personality"] != "balanced" {
		t.Fatalf("ReadRuntimeConfig() = %#v", got)
	}
	toolRouting, _ := got["toolRouting"].(map[string]any)
	if toolRouting["mode"] != "legacy" || toolRouting["routerProvider"] != "openai_compatible" {
		t.Fatalf("toolRouting fallback = %#v", toolRouting)
	}
}

func TestServiceSetConfigPersistsOverrideForOfflineReadback(t *testing.T) {
	t.Parallel()

	model := "gpt-5.5"
	effort := "high"
	approvals := "never"
	personality := "balanced"
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-1",
		Model:     "o4-mini",
		Cwd:       "/tmp/demo",
		Status:    statusCreated,
		CreatedAt: 100,
		UpdatedAt: 100,
	}}
	session := &stubSession{
		threadID:      "thread-1",
		allowedModels: []string{model},
		readConfigResult: dto.ThreadConfig{
			ThreadID:               "thread-1",
			Provider:               "codex",
			SupportsThreadOverride: true,
			Override: dto.ThreadConfigValues{
				Model:     model,
				Effort:    effort,
				Approvals: approvals,
			},
			Effective: dto.ThreadConfigValues{
				Model:     model,
				Effort:    effort,
				Approvals: approvals,
			},
		},
	}
	svc, ok := NewService(
		silentLogger(),
		threads,
		&stubBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "thread-1",
			CodexThreadID:    "thread-1",
		}},
		&stubSessionProvider{session: session},
		nil,
		nil,
		nil,
		nil,
	).(*service)
	if !ok {
		t.Fatal("NewService() type assertion failed")
	}

	got, err := svc.SetConfig(context.Background(), "thread-1", dto.ThreadConfigPatch{
		Model:       &model,
		Effort:      &effort,
		Approvals:   &approvals,
		Personality: &personality,
	})
	if err != nil {
		t.Fatalf("SetConfig() error = %v", err)
	}
	if got.Effective.Model != model || got.Override.Approvals != approvals {
		t.Fatalf("SetConfig() = %#v", got)
	}
	if stored := decodeStoredThreadConfig(threads.thread.ConfigOverride); stored.Model != model || stored.Personality != personality {
		t.Fatalf("stored override = %#v", stored)
	}
	if threads.thread.Model != model {
		t.Fatalf("stored model = %q, want %q", threads.thread.Model, model)
	}

	svc.sessions = &stubSessionProvider{}
	offlineCfg, err := svc.GetConfig(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("GetConfig() offline error = %v", err)
	}
	if offlineCfg.Override.Model != model || offlineCfg.Override.Effort != effort || offlineCfg.Override.Approvals != approvals {
		t.Fatalf("offline config = %#v", offlineCfg)
	}

	runtimeCfg, err := svc.ReadRuntimeConfig(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("ReadRuntimeConfig() offline error = %v", err)
	}
	if runtimeCfg["approvalPolicy"] != approvals || runtimeCfg["personality"] != personality {
		t.Fatalf("offline runtime config = %#v", runtimeCfg)
	}
}

func TestPersistThreadConfigModelPatchSemantics(t *testing.T) {
	t.Parallel()

	ptr := func(s string) *string { return &s }
	cases := []struct {
		name      string
		patch     dto.ThreadConfigPatch
		wantModel string
		wantStore string
	}{
		{name: "nil keeps existing", patch: dto.ThreadConfigPatch{}, wantModel: "keep-model", wantStore: "keep-model"},
		{name: "empty clears override", patch: dto.ThreadConfigPatch{Model: ptr("")}, wantModel: "", wantStore: ""},
		{name: "value updates model", patch: dto.ThreadConfigPatch{Model: ptr("gpt-5.5")}, wantModel: "gpt-5.5", wantStore: "gpt-5.5"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			threads := &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-1", Model: "keep-model", Status: statusCreated, CreatedAt: 1, UpdatedAt: 1, ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Model: "keep-model"})}}
			svc := NewService(silentLogger(), threads, nil, nil, nil, nil, nil, nil).(*service)
			if err := svc.persistThreadConfig(context.Background(), "thread-1", tt.patch, dto.ThreadConfig{}); err != nil {
				t.Fatalf("persistThreadConfig() error = %v", err)
			}
			if threads.thread.Model != tt.wantModel {
				t.Fatalf("thread.Model = %q, want %q", threads.thread.Model, tt.wantModel)
			}
			if got := decodeStoredThreadConfig(threads.thread.ConfigOverride).Model; got != tt.wantStore {
				t.Fatalf("stored override model = %q, want %q", got, tt.wantStore)
			}
		})
	}
}

func mustStoredThreadConfigRaw(
	t *testing.T,
	cfg storedThreadConfig,
) []byte {
	t.Helper()
	raw, err := encodeStoredThreadConfig(cfg)
	if err != nil {
		t.Fatalf("encodeStoredThreadConfig() error = %v", err)
	}
	return raw
}

func TestBuildOfflineRuntimeConfigIncludesModel(t *testing.T) {
	threads := &stubThreadStore{
		thread: &threadstore.Thread{
			ThreadID: "thread-model-offline",
			Model:    "claude-sonnet-4-20250514",
			Status:   "running",
		},
	}
	svc, ok := NewService(
		silentLogger(),
		threads,
		&stubBindingStore{binding: &bindingstore.Binding{}},
		&stubSessionProvider{session: nil},
		nil,
		nil,
		nil,
		nil,
	).(*service)
	if !ok {
		t.Fatal("NewService() type assertion failed")
	}

	runtime, err := svc.ReadRuntimeConfig(context.Background(), "thread-model-offline")
	if err != nil {
		t.Fatalf("ReadRuntimeConfig() error = %v", err)
	}

	model, ok := runtime["model"]
	if !ok {
		t.Fatalf("offline runtime should contain model field: %#v", runtime)
	}
	if model != "claude-sonnet-4-20250514" {
		t.Fatalf("runtime model = %#v, want %q", model, "claude-sonnet-4-20250514")
	}
}

func TestSetConfigFallsBackToOfflinePersistWithoutSession(t *testing.T) {
	t.Parallel()

	model := "gpt-5.5"
	effort := "high"
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-1",
		Model:     "o4-mini",
		Cwd:       "/tmp/demo",
		Status:    statusCreated,
		CreatedAt: 100,
		UpdatedAt: 100,
	}}
	// No session — stubSessionProvider with nil session returns ErrSessionNotFound.
	svc, ok := NewService(
		silentLogger(),
		threads,
		&stubBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "thread-1",
			CodexThreadID:    "thread-1",
		}},
		&stubSessionProvider{},
		nil,
		nil,
		nil,
		nil,
	).(*service)
	if !ok {
		t.Fatal("NewService() type assertion failed")
	}

	// SetConfig should succeed despite no session.
	got, err := svc.SetConfig(context.Background(), "thread-1", dto.ThreadConfigPatch{
		Model:  &model,
		Effort: &effort,
	})
	if err != nil {
		t.Fatalf("SetConfig() error = %v, want nil (offline fallback)", err)
	}
	if got.Override.Model != model {
		t.Fatalf("Override.Model = %q, want %q", got.Override.Model, model)
	}
	if got.Override.Effort != effort {
		t.Fatalf("Override.Effort = %q, want %q", got.Override.Effort, effort)
	}
	// Verify persisted.
	stored := decodeStoredThreadConfig(threads.thread.ConfigOverride)
	if stored.Model != model || stored.Effort != effort {
		t.Fatalf("stored override = %#v, want model=%q effort=%q", stored, model, effort)
	}
	if threads.thread.Model != model {
		t.Fatalf("stored thread model = %q, want %q", threads.thread.Model, model)
	}
	// Verify readback via GetConfig (also offline) returns correct values.
	offlineCfg, err := svc.GetConfig(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("GetConfig() offline error = %v", err)
	}
	if offlineCfg.Override.Model != model || offlineCfg.Override.Effort != effort {
		t.Fatalf("offline readback = %#v", offlineCfg)
	}
}

func TestSetConfigOfflineRejectsInvalidEffort(t *testing.T) {
	t.Parallel()

	effort := "turbo"
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-1",
		Model:     "o4-mini",
		Status:    statusCreated,
		CreatedAt: 100,
		UpdatedAt: 100,
	}}
	svc, ok := NewService(
		silentLogger(),
		threads,
		&stubBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "thread-1",
			CodexThreadID:    "thread-1",
		}},
		&stubSessionProvider{},
		nil,
		nil,
		nil,
		nil,
	).(*service)
	if !ok {
		t.Fatal("NewService() type assertion failed")
	}

	_, err := svc.SetConfig(context.Background(), "thread-1", dto.ThreadConfigPatch{
		Effort: &effort,
	})
	if err == nil {
		t.Fatal("SetConfig() error = nil, want invalid effort error")
	}
}

