package thread

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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
	if cfg.Effective.Model != "gpt-5.5" || cfg.Override.Approvals != "on-request" {
		t.Fatalf("GetConfig() = %#v", cfg)
	}
}

func TestServiceGetConfigFallsBackToOfflineConfigWithoutSession(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:       "thread-1",
		Model:          "gpt-5.5",
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
	if cfg.Effective.Model != "gpt-5.5" || cfg.Override.Effort != "high" || cfg.Override.Approvals != "never" {
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

func TestBuildOfflineConfigPrefersStoredProviderOverBinding(t *testing.T) {
	t.Parallel()

	got := mustBuildOfflineConfig(t, &threadstore.Thread{
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

	got := mustBuildOfflineConfig(t, &threadstore.Thread{
		ThreadID:       "thread-provider-mismatch",
		Model:          "gpt-5.5",
		Status:         statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Provider: "claude"}),
	}, &bindingstore.Binding{Provider: "codex"})

	if got.Config.Provider != "claude" {
		t.Fatalf("buildOfflineConfig() provider = %q, want claude", got.Config.Provider)
	}
}

func TestBuildOfflineConfigFallsBackToBindingWhenStoredEmpty(t *testing.T) {
	t.Parallel()

	got := mustBuildOfflineConfig(t, &threadstore.Thread{
		ThreadID:       "thread-binding-fallback",
		Model:          "gpt-5.5",
		Status:         statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{}),
	}, &bindingstore.Binding{Provider: "codex"})

	if got.Config.Provider != "codex" {
		t.Fatalf("buildOfflineConfig() provider = %q, want codex", got.Config.Provider)
	}
}

func TestBuildOfflineConfigFallsBackToDefaultWhenAllEmpty(t *testing.T) {
	t.Parallel()

	got := mustBuildOfflineConfig(t, &threadstore.Thread{
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

	got := mustBuildOfflineConfig(t, &threadstore.Thread{
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
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:       "thread-1",
		Model:          "gpt-5.5",
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

func mustBuildOfflineConfig(
	t *testing.T,
	thread *threadstore.Thread,
	binding *bindingstore.Binding,
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

func TestReadRuntimeConfigIncludesStoredPromptContext(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID: "thread-context-offline",
		Model:    "gpt-5.5",
		Cwd:      "/repo",
		Status:   "running",
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Runtime: map[string]any{
			"provider":                     "codex",
			"gitRoot":                      "/repo",
			"isWorktree":                   true,
			"language":                     "Chinese",
			"enabledTools":                 []any{"lsp_file", "lsp_grep"},
			"additionalWorkingDirectories": []any{"/repo/extra"},
			"mcpTools":                     []any{"mcp__lsp__lsp_grep"},
			"mcpInstructions":              map[string]any{"lsp": "Use the LSP thread fallback."},
			"sessionFlags":                 map[string]any{"verification_required": true},
		}}),
	}}
	svc, ok := NewService(
		silentLogger(),
		threads,
		&stubBindingStore{binding: &bindingstore.Binding{Provider: "codex"}},
		&stubSessionProvider{session: nil},
		nil,
		nil,
		nil,
		nil,
	).(*service)
	if !ok {
		t.Fatal("NewService() type assertion failed")
	}

	runtime, err := svc.ReadRuntimeConfig(context.Background(), "thread-context-offline")
	if err != nil {
		t.Fatalf("ReadRuntimeConfig() error = %v", err)
	}
	if runtime["gitRoot"] != "/repo" || runtime["language"] != "Chinese" || runtime["provider"] != "codex" {
		t.Fatalf("offline runtime context = %#v", runtime)
	}
	if runtime["isWorktree"] != true {
		t.Fatalf("offline runtime isWorktree = %#v, want true", runtime["isWorktree"])
	}
	if got, ok := runtime["mcpInstructions"].(map[string]any); !ok || got["lsp"] != "Use the LSP thread fallback." {
		t.Fatalf("offline runtime mcpInstructions = %#v", runtime["mcpInstructions"])
	}
	if got, ok := runtime["sessionFlags"].(map[string]any); !ok || got["verification_required"] != true {
		t.Fatalf("offline runtime sessionFlags = %#v", runtime["sessionFlags"])
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

func TestSetConfigFallsBackToOfflinePersistWithoutBinding(t *testing.T) {
	t.Parallel()

	model := "claude-opus-4-7[1m]"
	effort := "max"
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:      "thread-pending-claude",
		Model:         "sonnet",
		Status:        statusCreated,
		PendingLaunch: true,
		CreatedAt:     100,
		UpdatedAt:     100,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{
			Provider: "claude",
		}),
	}}
	svc, ok := NewService(
		silentLogger(),
		threads,
		&stubBindingStore{},
		&stubSessionProvider{},
		nil,
		nil,
		nil,
		nil,
	).(*service)
	if !ok {
		t.Fatal("NewService() type assertion failed")
	}

	got, err := svc.SetConfig(context.Background(), "thread-pending-claude", dto.ThreadConfigPatch{
		Model:  &model,
		Effort: &effort,
	})
	if err != nil {
		t.Fatalf("SetConfig() error = %v, want nil (offline fallback without binding)", err)
	}
	if got.Provider != "claude" {
		t.Fatalf("Provider = %q, want claude", got.Provider)
	}
	if got.Override.Model != model || got.Override.Effort != effort {
		t.Fatalf("Override = %#v, want model=%q effort=%q", got.Override, model, effort)
	}
	stored := decodeStoredThreadConfig(threads.thread.ConfigOverride)
	if stored.Provider != "claude" || stored.Model != model || stored.Effort != effort {
		t.Fatalf("stored override = %#v, want provider/model/effort preserved", stored)
	}
	if threads.thread.Model != model {
		t.Fatalf("stored thread model = %q, want %q", threads.thread.Model, model)
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

func TestServiceReadRuntimeConfigsMergesBatch(t *testing.T) {
	t.Parallel()

	session1 := &stubSession{
		threadID: "thread-1",
		runtimeConfig: map[string]any{
			"approvalPolicy": "on-request",
		},
	}
	session2 := &stubSession{
		threadID: "thread-2",
		runtimeConfig: map[string]any{
			"approvalPolicy": "never",
		},
	}
	threads := &stubThreadStore{
		thread: &threadstore.Thread{
			ThreadID:       "thread-1",
			Model:          "gpt-5.5",
			AgentID:        "agent-1",
			ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Personality: "balanced", Approvals: "never"}),
		},
		threads: []threadstore.Thread{
			{
				ThreadID:       "thread-1",
				Model:          "gpt-5.5",
				AgentID:        "agent-1",
				ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Personality: "balanced", Approvals: "never"}),
			},
			{
				ThreadID:       "thread-2",
				Model:          "claude-opus",
				AgentID:        "agent-2",
				ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Personality: "creative"}),
			},
			{
				ThreadID: "thread-3",
				AgentID:  "agent-3",
			},
		},
		threadByID: map[string]*threadstore.Thread{
			"thread-1": {
				ThreadID:       "thread-1",
				Model:          "gpt-5.5",
				AgentID:        "agent-1",
				ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Personality: "balanced", Approvals: "never"}),
			},
			"thread-2": {
				ThreadID:       "thread-2",
				Model:          "claude-opus",
				AgentID:        "agent-2",
				ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Personality: "creative"}),
			},
			"thread-3": {
				ThreadID: "thread-3",
				AgentID:  "agent-3",
			},
		},
	}
	bindings := []bindingstore.Binding{
		{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "thread-1",
		},
		{
			AgentID:          "agent-2",
			Provider:         "claude",
			ProviderThreadID: "thread-2",
		},
	}
	svc, ok := NewService(
		silentLogger(),
		threads,
		&stubBindingStore{bindings: bindings},
		&stubSessionProvider{
			session: session1, // For default
			sessions: map[string]contract.Session{
				"agent-1": session1,
				"agent-2": session2,
			},
		},
		nil,
		nil,
		nil,
		nil,
	).(*service)
	if !ok {
		t.Fatal("NewService() type assertion failed")
	}

	gotMap, err := svc.ReadRuntimeConfigs(context.Background(), []string{"thread-1", "thread-2", "thread-3", "thread-4"})
	if err != nil {
		t.Fatalf("ReadRuntimeConfigs() error = %v", err)
	}

	if len(gotMap) != 4 {
		t.Fatalf("ReadRuntimeConfigs() expected 4 results, got %d", len(gotMap))
	}

	got1 := gotMap["thread-1"]
	if got1["approvalPolicy"] != "on-request" || got1["personality"] != "balanced" {
		t.Fatalf("ReadRuntimeConfigs()[thread-1] = %#v", got1)
	}

	got2 := gotMap["thread-2"]
	if got2["approvalPolicy"] != "never" || got2["personality"] != "creative" {
		t.Fatalf("ReadRuntimeConfigs()[thread-2] = %#v", got2)
	}

	got3 := gotMap["thread-3"]
	if got3["approvalPolicy"] != "on-failure" || got3["model"] != nil {
		t.Fatalf("ReadRuntimeConfigs()[thread-3] = %#v", got3)
	}

	got4 := gotMap["thread-4"]
	if got4["approvalPolicy"] != "on-failure" {
		t.Fatalf("ReadRuntimeConfigs()[thread-4] = %#v", got4)
	}
}
