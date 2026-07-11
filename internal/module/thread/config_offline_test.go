package thread

import (
	"context"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestServiceGetConfigPrefersSessionValueOverOfflineOverride(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
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
				Model:     "gpt-5.5",
				Effort:    "high",
				Approvals: "on-request",
			},
			Effective: dto.ThreadConfigValues{
				Model:     "gpt-5.5",
				Effort:    "high",
				Approvals: "on-request",
			},
		},
	}
	svc := NewService(
		silentLogger(),
		threads,
		&stubBindingStore{binding: &BindingRecord{
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
	if cfg.Effective.Model != "gpt-5.5" || cfg.Override.Approvals != "on-request" {
		t.Fatalf("GetConfig() = %#v", cfg)
	}
}

func TestServiceGetConfigFailsFastWithoutSessionReturnsOfflineConfig(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-1",
		Model:          "gpt-5.5",
		Cwd:            "/tmp/demo",
		Status:         statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Effort: "high", Approvals: "never", Personality: "balanced"}),
	}}
	svc := newConfigTestService(t, threads, testCodexBindingStore(), &stubSessionProvider{})

	cfg, err := svc.GetConfig(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("GetConfig() error = %v, want offline config", err)
	}
	assertOfflineConfigFallback(t, cfg)

	runtimeCfg, err := svc.ReadRuntimeConfig(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("ReadRuntimeConfig() error = %v, want offline runtime config", err)
	}
	assertOfflineRuntimeReadback(t, runtimeCfg, "never", "balanced")
	if runtimeCfg["model"] != "gpt-5.5" || runtimeCfg["cwd"] != "/tmp/demo" {
		t.Fatalf("offline runtime config = %#v", runtimeCfg)
	}
}

func TestServiceGetConfigPendingLaunchReturnsOfflineConfigWithoutBindingOrSession(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:      "thread-pending-launch",
		Model:         "stored-thread-model",
		Status:        statusCreated,
		PendingLaunch: true,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{
			Provider:  "codex",
			Model:     "gpt-5.5",
			Effort:    "high",
			Approvals: "never",
		}),
	}}
	svc := newConfigTestService(t, threads, &stubBindingStore{}, &stubSessionProvider{})

	cfg, err := svc.GetConfig(context.Background(), "thread-pending-launch")
	if err != nil {
		t.Fatalf("GetConfig() error = %v, want pending_launch offline config", err)
	}
	if cfg.Provider != "codex" || cfg.Override.Model != "gpt-5.5" || cfg.Override.Effort != "high" || cfg.Override.Approvals != "never" {
		t.Fatalf("GetConfig() override = %#v, provider = %q", cfg.Override, cfg.Provider)
	}
	if cfg.Effective.Model != "gpt-5.5" || cfg.Effective.Effort != "high" || cfg.Effective.Approvals != "never" {
		t.Fatalf("GetConfig() effective = %#v", cfg.Effective)
	}
}

func TestBuildOfflineConfigPrefersStoredProviderOverBinding(t *testing.T) {
	t.Parallel()

	got := mustBuildOfflineConfig(t, &ThreadRecord{
		ThreadID:       "thread-stored-provider",
		Model:          "gpt-5.5",
		Status:         statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Provider: "claude"}),
	}, nil)

	if got.Config.Provider != "claude" {
		t.Fatalf("buildOfflineConfig() provider = %q, want claude", got.Config.Provider)
	}
}

func TestBuildOfflineConfigPrefersStoredProviderWhenBindingMismatch(t *testing.T) {
	t.Parallel()

	got := mustBuildOfflineConfig(t, &ThreadRecord{
		ThreadID:       "thread-provider-mismatch",
		Model:          "gpt-5.5",
		Status:         statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Provider: "claude"}),
	}, &BindingRecord{Provider: "codex"})

	if got.Config.Provider != "claude" {
		t.Fatalf("buildOfflineConfig() provider = %q, want claude", got.Config.Provider)
	}
}

func TestBuildOfflineConfigFallsBackToBindingWhenStoredEmpty(t *testing.T) {
	t.Parallel()

	got := mustBuildOfflineConfig(t, &ThreadRecord{
		ThreadID:       "thread-binding-fallback",
		Model:          "gpt-5.5",
		Status:         statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{}),
	}, &BindingRecord{Provider: "codex"})

	if got.Config.Provider != "codex" {
		t.Fatalf("buildOfflineConfig() provider = %q, want codex", got.Config.Provider)
	}
}

func TestBuildOfflineConfigFallsBackToDefaultWhenAllEmpty(t *testing.T) {
	t.Parallel()

	got := mustBuildOfflineConfig(t, &ThreadRecord{
		ThreadID:       "thread-default-fallback",
		Model:          "gpt-5.5",
		Status:         statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{}),
	}, nil)

	if got.Config.Provider != "codex" {
		t.Fatalf("buildOfflineConfig() provider = %q, want codex", got.Config.Provider)
	}
}

func TestBuildOfflineConfigPendingLaunchClaudeThread(t *testing.T) {
	t.Parallel()

	got := mustBuildOfflineConfig(t, &ThreadRecord{
		ThreadID:      "thread-pending-claude",
		Prompt:        "",
		Status:        statusCreated,
		PendingLaunch: true,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{
			Provider:    "claude",
			Effort:      "high",
			Approvals:   "never",
			Personality: "balanced",
		}),
	}, nil)

	if got.Config.Provider != "claude" {
		t.Fatalf("buildOfflineConfig() provider = %q, want claude", got.Config.Provider)
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
	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-1",
		Model:          "gpt-5.5",
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Personality: "balanced", Approvals: "never"}),
	}}
	svc, ok := NewService(
		silentLogger(),
		threads,
		&stubBindingStore{binding: &BindingRecord{
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
	threads := newConfigPersistenceThreadStore()
	session := newConfigPersistenceSession(model, effort, approvals)
	svc := newConfigTestService(t, threads, testCodexBindingStore(), &stubSessionProvider{session: session})

	got, err := svc.SetConfig(context.Background(), "thread-1", dto.ThreadConfigPatch{
		Model:       &model,
		Effort:      &effort,
		Approvals:   &approvals,
		Personality: &personality,
	})
	if err != nil {
		t.Fatalf("SetConfig() error = %v", err)
	}
	assertSetConfigResult(t, got, model, approvals)
	assertStoredOverrideAndModel(t, threads, model, personality)

	activeCfg, err := svc.GetConfig(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	assertOfflineOverrideReadback(t, activeCfg, model, effort, approvals)

	runtimeCfg, err := svc.ReadRuntimeConfig(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("ReadRuntimeConfig() error = %v", err)
	}
	assertOfflineRuntimeReadback(t, runtimeCfg, approvals, personality)

	svc.sessions = &stubSessionProvider{}
	offlineCfg, err := svc.GetConfig(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("GetConfig() after session removal error = %v, want offline config", err)
	}
	assertOfflineOverrideReadback(t, offlineCfg, model, effort, approvals)
	runtimeCfg, err = svc.ReadRuntimeConfig(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("ReadRuntimeConfig() after session removal error = %v, want offline runtime config", err)
	}
	assertOfflineRuntimeReadback(t, runtimeCfg, approvals, personality)
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
			threads := &stubThreadStore{thread: &ThreadRecord{ThreadID: "thread-1", Model: "keep-model", Status: statusCreated, CreatedAt: 1, UpdatedAt: 1, ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Model: "keep-model"})}}
			svc := NewService(silentLogger(), threads, nil, nil, nil, nil, nil, nil).(*service)
			if err := svc.persistThreadConfig(context.Background(), "thread-1", tt.patch, dto.ThreadConfig{}); err != nil {
				t.Fatalf("persistThreadConfig() error = %v", err)
			}
			if threads.thread.Model != tt.wantModel {
				t.Fatalf("thread.Model = %q, want %q", threads.thread.Model, tt.wantModel)
			}
			if got := mustDecodeStoredThreadConfig(t, threads.thread.ConfigOverride).Model; got != tt.wantStore {
				t.Fatalf("stored override model = %q, want %q", got, tt.wantStore)
			}
		})
	}
}

func mustBuildOfflineConfig(
	t *testing.T,
	thread *ThreadRecord,
	binding *BindingRecord,
) offlineConfigSnapshot {
	t.Helper()
	svc := NewService(silentLogger(), &stubThreadStore{thread: thread}, nil, nil, nil, nil, nil, nil).(*service)
	got, err := svc.buildOfflineConfig(context.Background(), thread.ThreadID, binding)
	if err != nil {
		t.Fatalf("buildOfflineConfig() error = %v", err)
	}
	return got
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

func newConfigTestService(
	t *testing.T,
	threads *stubThreadStore,
	bindings *stubBindingStore,
	sessions *stubSessionProvider,
) *service {
	t.Helper()
	svc, ok := NewService(silentLogger(), threads, bindings, sessions, nil, nil, nil, nil).(*service)
	if !ok {
		t.Fatal("NewService() type assertion failed")
	}
	return svc
}

func testCodexBindingStore() *stubBindingStore {
	return &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "thread-1",
		CodexThreadID:    "thread-1",
	}}
}

func assertOfflineConfigFallback(t *testing.T, cfg dto.ThreadConfig) {
	t.Helper()
	if !cfg.SupportsThreadOverride || cfg.Provider != "codex" {
		t.Fatalf("GetConfig() provider = %#v", cfg)
	}
	if cfg.Effective.Model != "gpt-5.5" || cfg.Override.Effort != "high" || cfg.Override.Approvals != "never" {
		t.Fatalf("GetConfig() offline = %#v", cfg)
	}
}

func newConfigPersistenceThreadStore() *stubThreadStore {
	return &stubThreadStore{thread: &ThreadRecord{
		ThreadID:  "thread-1",
		Model:     "o4-mini",
		Cwd:       "/tmp/demo",
		Status:    statusCreated,
		CreatedAt: 100,
		UpdatedAt: 100,
	}}
}

func newConfigPersistenceSession(model, effort, approvals string) *stubSession {
	return &stubSession{
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
}

func assertSetConfigResult(t *testing.T, got dto.ThreadConfig, model string, approvals string) {
	t.Helper()
	if got.Effective.Model != model || got.Override.Approvals != approvals {
		t.Fatalf("SetConfig() = %#v", got)
	}
}

func assertStoredOverrideAndModel(t *testing.T, threads *stubThreadStore, model string, personality string) {
	t.Helper()
	if stored := mustDecodeStoredThreadConfig(t, threads.thread.ConfigOverride); stored.Model != model || stored.Personality != personality {
		t.Fatalf("stored override = %#v", stored)
	}
	if threads.thread.Model != model {
		t.Fatalf("stored model = %q, want %q", threads.thread.Model, model)
	}
}

func assertOfflineOverrideReadback(
	t *testing.T,
	offlineCfg dto.ThreadConfig,
	model string,
	effort string,
	approvals string,
) {
	t.Helper()
	if offlineCfg.Override.Model != model || offlineCfg.Override.Effort != effort || offlineCfg.Override.Approvals != approvals {
		t.Fatalf("offline config = %#v", offlineCfg)
	}
}

func assertOfflineRuntimeReadback(t *testing.T, runtimeCfg map[string]any, approvals string, personality string) {
	t.Helper()
	if runtimeCfg["approvalPolicy"] != approvals || runtimeCfg["personality"] != personality {
		t.Fatalf("offline runtime config = %#v", runtimeCfg)
	}
}
