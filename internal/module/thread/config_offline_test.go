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
