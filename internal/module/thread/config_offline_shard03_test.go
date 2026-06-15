package thread

import (
	"context"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestBuildOfflineRuntimeConfigIncludesModel(t *testing.T) {
	threads := &stubThreadStore{
		thread: &threadstore.Thread{
			ThreadID: "thread-model-offline",
			Model:    "claude-sonnet-4-20250514",
			Status:   "running",
		},
	}
	runtime := mustBuildOfflineConfig(t, threads.thread, &bindingstore.Binding{Provider: "claude"}).Runtime

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
	svc := newConfigTestService(t, threads, &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "thread-context-offline",
		CodexThreadID:    "thread-context-offline",
	}}, &stubSessionProvider{session: &stubSession{threadID: "thread-context-offline"}})

	runtime, err := svc.ReadRuntimeConfig(context.Background(), "thread-context-offline")
	if err != nil {
		t.Fatalf("ReadRuntimeConfig() error = %v", err)
	}
	assertStoredPromptRuntimeContext(t, runtime)
}

func assertStoredPromptRuntimeContext(t *testing.T, runtime map[string]any) {
	t.Helper()
	if runtime["gitRoot"] != "/repo" || runtime["language"] != "Chinese" || runtime["provider"] != "codex" {
		t.Fatalf("offline runtime context = %#v", runtime)
	}
	if runtime["isWorktree"] != true {
		t.Fatalf("offline runtime isWorktree = %#v, want true", runtime["isWorktree"])
	}
	assertRuntimeNestedValue(t, runtime, "mcpInstructions", "lsp", "Use the LSP thread fallback.")
	assertRuntimeNestedValue(t, runtime, "sessionFlags", "verification_required", true)
}

func assertRuntimeNestedValue(t *testing.T, runtime map[string]any, outer string, inner string, want any) {
	t.Helper()
	got, ok := runtime[outer].(map[string]any)
	if !ok || got[inner] != want {
		t.Fatalf("offline runtime %s = %#v", outer, runtime[outer])
	}
}

func TestSetConfigFailsFastWithoutSession(t *testing.T) {
	t.Parallel()

	model := "gpt-5.5"
	effort := "high"
	threads := newConfigPersistenceThreadStore()
	svc := newConfigTestService(t, threads, testCodexBindingStore(), &stubSessionProvider{})

	_, err := svc.SetConfig(context.Background(), "thread-1", dto.ThreadConfigPatch{
		Model:  &model,
		Effort: &effort,
	})
	if !errors.Is(err, contract.ErrSessionNotFound) {
		t.Fatalf("SetConfig() error = %v, want session not found", err)
	}
	if threads.thread.Model != "o4-mini" || len(threads.thread.ConfigOverride) != 0 {
		t.Fatalf("thread config mutated despite failed SetConfig: %#v", threads.thread)
	}
}

func assertOfflineSetConfigOverride(t *testing.T, got dto.ThreadConfig, model string, effort string) {
	t.Helper()
	if got.Override.Model != model {
		t.Fatalf("Override.Model = %q, want %q", got.Override.Model, model)
	}
	if got.Override.Effort != effort {
		t.Fatalf("Override.Effort = %q, want %q", got.Override.Effort, effort)
	}
}

func assertOfflineSetConfigStored(t *testing.T, threads *stubThreadStore, model string, effort string) {
	t.Helper()
	stored := mustDecodeStoredThreadConfig(t, threads.thread.ConfigOverride)
	if stored.Model != model || stored.Effort != effort {
		t.Fatalf("stored override = %#v, want model=%q effort=%q", stored, model, effort)
	}
	if threads.thread.Model != model {
		t.Fatalf("stored thread model = %q, want %q", threads.thread.Model, model)
	}
}

func assertOfflineSetConfigReadback(t *testing.T, offlineCfg dto.ThreadConfig, model string, effort string) {
	t.Helper()
	if offlineCfg.Override.Model != model || offlineCfg.Override.Effort != effort {
		t.Fatalf("offline readback = %#v", offlineCfg)
	}
}

func TestSetConfigFailsFastWithoutBinding(t *testing.T) {
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
	svc := newConfigTestService(t, threads, &stubBindingStore{}, &stubSessionProvider{})

	_, err := svc.SetConfig(context.Background(), "thread-pending-claude", dto.ThreadConfigPatch{
		Model:  &model,
		Effort: &effort,
	})
	if !contract.IsNotFound(err) {
		t.Fatalf("SetConfig() error = %v, want not found", err)
	}
	stored := mustDecodeStoredThreadConfig(t, threads.thread.ConfigOverride)
	if stored.Model != "" || stored.Effort != "" || threads.thread.Model != "sonnet" {
		t.Fatalf("thread config mutated despite failed SetConfig: thread=%#v stored=%#v", threads.thread, stored)
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
	svc := newConfigTestService(t, threads, testCodexBindingStore(), &stubSessionProvider{})

	_, err := svc.SetConfig(context.Background(), "thread-1", dto.ThreadConfigPatch{
		Effort: &effort,
	})
	if err == nil {
		t.Fatal("SetConfig() error = nil, want invalid effort error")
	}
}

func TestServiceReadRuntimeConfigsMergesBatch(t *testing.T) {
	t.Parallel()

	svc := newBatchRuntimeConfigService(t)
	gotMap, err := svc.ReadRuntimeConfigs(context.Background(), []string{"thread-1", "thread-2", "thread-3"})
	if err != nil {
		t.Fatalf("ReadRuntimeConfigs() error = %v", err)
	}
	if len(gotMap) != 3 {
		t.Fatalf("ReadRuntimeConfigs() expected 3 results, got %d", len(gotMap))
	}
	assertBatchRuntimeConfigResults(t, gotMap)
}

func TestServiceReadRuntimeConfigsUsesOfflineWhenBindingSessionMissing(t *testing.T) {
	t.Parallel()

	session1, _ := batchRuntimeSessions()
	svc := newConfigTestService(
		t,
		batchRuntimeThreadStore(t),
		&stubBindingStore{bindings: batchRuntimeBindings()},
		&stubSessionProvider{sessions: map[string]contract.Session{
			"agent-1": session1,
		}},
	)

	gotMap, err := svc.ReadRuntimeConfigs(context.Background(), []string{"thread-1", "thread-2", "thread-3"})
	if err != nil {
		t.Fatalf("ReadRuntimeConfigs() error = %v, want offline fallback for missing agent-2 session", err)
	}
	if len(gotMap) != 3 {
		t.Fatalf("ReadRuntimeConfigs() expected 3 results, got %d", len(gotMap))
	}
	assertRuntimeConfigFields(t, gotMap["thread-1"], "thread-1", "on-request", "balanced")
	assertRuntimeConfigFields(t, gotMap["thread-2"], "thread-2", "on-failure", "creative")
	if gotMap["thread-2"]["model"] != "claude-opus" {
		t.Fatalf("ReadRuntimeConfigs()[thread-2] = %#v", gotMap["thread-2"])
	}
	assertRuntimeConfigFields(t, gotMap["thread-3"], "thread-3", "on-failure", nil)
}

func newBatchRuntimeConfigService(t *testing.T) *service {
	t.Helper()
	session1, session2 := batchRuntimeSessions()
	return newConfigTestService(
		t,
		batchRuntimeThreadStore(t),
		&stubBindingStore{bindings: batchRuntimeBindings()},
		&stubSessionProvider{
			session: session1,
			sessions: map[string]contract.Session{
				"agent-1": session1,
				"agent-2": session2,
			},
		},
	)
}

func batchRuntimeSessions() (*stubSession, *stubSession) {
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
	return session1, session2
}

func batchRuntimeThreadStore(t *testing.T) *stubThreadStore {
	t.Helper()
	thread1 := threadstore.Thread{
		ThreadID:       "thread-1",
		Model:          "gpt-5.5",
		AgentID:        "agent-1",
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Personality: "balanced", Approvals: "never"}),
	}
	thread2 := threadstore.Thread{
		ThreadID:       "thread-2",
		Model:          "claude-opus",
		AgentID:        "agent-2",
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Personality: "creative"}),
	}
	thread3 := threadstore.Thread{ThreadID: "thread-3", PendingLaunch: true}
	return &stubThreadStore{
		thread:     cloneThread(thread1),
		threads:    []threadstore.Thread{thread1, thread2, thread3},
		threadByID: map[string]*threadstore.Thread{"thread-1": cloneThread(thread1), "thread-2": cloneThread(thread2), "thread-3": cloneThread(thread3)},
	}
}

func cloneThread(thread threadstore.Thread) *threadstore.Thread {
	copy := thread
	return &copy
}

func batchRuntimeBindings() []bindingstore.Binding {
	return []bindingstore.Binding{
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
}

func assertBatchRuntimeConfigResults(t *testing.T, gotMap map[string]map[string]any) {
	t.Helper()
	assertRuntimeConfigFields(t, gotMap["thread-1"], "thread-1", "on-request", "balanced")
	assertRuntimeConfigFields(t, gotMap["thread-2"], "thread-2", "never", "creative")
	assertRuntimeConfigFields(t, gotMap["thread-3"], "thread-3", "on-failure", nil)
	if gotMap["thread-3"]["model"] != nil {
		t.Fatalf("ReadRuntimeConfigs()[thread-3] = %#v", gotMap["thread-3"])
	}
}

func assertRuntimeConfigFields(t *testing.T, got map[string]any, threadID string, approval any, personality any) {
	t.Helper()
	if got["approvalPolicy"] != approval || got["personality"] != personality {
		t.Fatalf("ReadRuntimeConfigs()[%s] = %#v", threadID, got)
	}
}
