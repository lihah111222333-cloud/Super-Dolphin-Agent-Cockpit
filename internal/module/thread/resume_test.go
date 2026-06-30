package thread

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestServiceResumeInfersProviderAndRebuildsSession(t *testing.T) {
	t.Parallel()

	const providerThreadID = "11111111-2222-3333-4444-555555555551"
	rolloutPath := writeExistingProviderHistoryFile(t)
	snapshot := validThreadPromptSnapshotForTest("resume")
	threads := &stubThreadStore{
		thread: &threadstore.Thread{
			ThreadID:      "thread-1",
			AgentID:       "agent-1",
			Prompt:        "resume",
			Model:         "stored-model",
			Cwd:           "/repo",
			CreatedAt:     123,
			Status:        statusCreated,
			LastEventType: "",
			ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Runtime: map[string]any{
				"additionalWorkingDirectories": []any{"/repo/extra"},
			}}),
		},
		promptSnapshot: &snapshot,
	}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: providerThreadID,
		CodexThreadID:    "thread-1",
		RolloutPath:      rolloutPath,
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			assertResumeRebuildRequest(t, req, providerThreadID)
			session := &stubSession{threadID: providerThreadID, rolloutPath: rolloutPath}
			sessions.session = session
			return session, nil
		},
	}

	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)
	result, err := svc.Resume(context.Background(), ResumeRequest{
		ThreadID: "thread-1",
		Model:    "override-model",
	})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	assertResumeRebuildResult(t, result, providerThreadID)
	assertResumeRebuildSideEffects(t, sessions, threads, bindings, orch)
}

func TestServiceResumeHydratesCodexDisabledNativeToolsFromRuntime(t *testing.T) {
	t.Parallel()

	reqCh := make(chan dto.ResumeSessionRequest, 1)
	svc := newResumeCodexDisabledNativeToolsService(t, map[string]any{
		"codexDisabledNativeTools": []any{"shell", "apply_patch", "shell"},
	}, func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		reqCh <- req
		return &stubSession{
			threadID:    "11111111-2222-3333-4444-555555555552",
			rolloutPath: writeExistingProviderHistoryFile(t),
		}, nil
	})

	if _, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-native-tools"}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	select {
	case req := <-reqCh:
		want := []string{"apply_patch", "shell"}
		if !reflect.DeepEqual(req.CodexDisabledNativeTools, want) {
			t.Fatalf("CodexDisabledNativeTools = %#v, want %#v", req.CodexDisabledNativeTools, want)
		}
	default:
		t.Fatal("ResumeSession was not called")
	}
}

func TestServiceResumeRejectsMalformedRuntimeCodexDisabledNativeTools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		value       any
		wantType    string
		wantElement bool
	}{
		{name: "mixed_array", value: []any{"shell", 42}, wantType: "float64", wantElement: true},
		{name: "object", value: map[string]any{"tool": "shell"}, wantType: "map[string]interface {}"},
		{name: "integer", value: 42, wantType: "float64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resumeCalled := false
			svc := newResumeCodexDisabledNativeToolsService(t, map[string]any{
				"codexDisabledNativeTools": tt.value,
			}, func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
				resumeCalled = true
				t.Fatal("ResumeSession should not be called with malformed codexDisabledNativeTools runtime config")
				return nil, nil
			})

			_, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-native-tools"})
			if err == nil {
				t.Fatal("Resume() error = nil, want malformed codexDisabledNativeTools error")
			}
			if !strings.Contains(err.Error(), "codexDisabledNativeTools") || !strings.Contains(err.Error(), tt.wantType) {
				t.Fatalf("Resume() error = %v, want codexDisabledNativeTools and %q", err, tt.wantType)
			}
			if tt.wantElement && !strings.Contains(err.Error(), "contains") {
				t.Fatalf("Resume() error = %v, want element type context", err)
			}
			if resumeCalled {
				t.Fatal("ResumeSession was called despite malformed codexDisabledNativeTools runtime config")
			}
		})
	}
}

func TestServiceResumeTypedRequestCodexDisabledNativeToolsTakesPrecedence(t *testing.T) {
	t.Parallel()

	reqCh := make(chan dto.ResumeSessionRequest, 1)
	svc := newResumeCodexDisabledNativeToolsService(t, map[string]any{
		"codexDisabledNativeTools": []any{"shell", 42},
	}, func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		reqCh <- req
		return &stubSession{
			threadID:    "11111111-2222-3333-4444-555555555552",
			rolloutPath: writeExistingProviderHistoryFile(t),
		}, nil
	})

	// 显式请求字段已经由 Go 类型系统约束为 []string，不来自 runtime map；因此仍优先于持久化 runtime。
	_, err := svc.Resume(context.Background(), ResumeRequest{
		ThreadID:                 "thread-native-tools",
		CodexDisabledNativeTools: []string{"shell"},
	})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	select {
	case req := <-reqCh:
		if want := []string{"shell"}; !reflect.DeepEqual(req.CodexDisabledNativeTools, want) {
			t.Fatalf("CodexDisabledNativeTools = %#v, want %#v", req.CodexDisabledNativeTools, want)
		}
	default:
		t.Fatal("ResumeSession was not called")
	}
}

func TestServiceResumeRejectsRequestCWDThatDiffersFromStoredThreadCWD(t *testing.T) {
	t.Parallel()

	const providerThreadID = "11111111-2222-3333-4444-555555555557"
	rolloutPath := writeExistingProviderHistoryFile(t)
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-1",
		AgentID:   "agent-1",
		Prompt:    "resume",
		Model:     "stored-model",
		Cwd:       "/repo/stored",
		CreatedAt: 123,
		Status:    statusCreated,
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: providerThreadID,
		CodexThreadID:    "thread-1",
		RolloutPath:      rolloutPath,
		Cwd:              "/repo/stored",
	}}
	starter := &stubSessionStarter{
		onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
			t.Fatal("ResumeSession should not be called when request cwd differs from stored cwd")
			return nil, nil
		},
	}
	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, starter, nil, nil, nil).(*service)

	_, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-1", CWD: "/repo/active-window"})
	if err == nil {
		t.Fatal("Resume() error = nil, want cwd mismatch error")
	}
	if !strings.Contains(err.Error(), "cwd mismatch") || !strings.Contains(err.Error(), "/repo/stored") || !strings.Contains(err.Error(), "/repo/active-window") {
		t.Fatalf("Resume() error = %v, want mismatch with both cwd values", err)
	}
}

func TestServiceResumeRejectsMissingStoredCWD(t *testing.T) {
	t.Parallel()

	const providerThreadID = "11111111-2222-3333-4444-555555555558"
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-1",
		AgentID:   "agent-1",
		Prompt:    "resume",
		Model:     "stored-model",
		CreatedAt: 123,
		Status:    statusCreated,
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: providerThreadID,
		CodexThreadID:    "thread-1",
		RolloutPath:      writeExistingProviderHistoryFile(t),
	}}
	starter := &stubSessionStarter{
		onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
			t.Fatal("ResumeSession should not be called when no authoritative cwd exists")
			return nil, nil
		},
	}
	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, starter, nil, nil, nil).(*service)

	_, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-1"})
	if err == nil {
		t.Fatal("Resume() error = nil, want missing cwd error")
	}
	if !strings.Contains(err.Error(), "cwd is required") {
		t.Fatalf("Resume() error = %v, want cwd required", err)
	}
}

func TestServiceResumeRejectsRequestCWDWhenStoredCWDIsMissing(t *testing.T) {
	t.Parallel()

	const providerThreadID = "11111111-2222-3333-4444-555555555559"
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-1",
		AgentID:   "agent-1",
		Prompt:    "resume",
		Model:     "stored-model",
		CreatedAt: 123,
		Status:    statusCreated,
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: providerThreadID,
		CodexThreadID:    "thread-1",
		RolloutPath:      writeExistingProviderHistoryFile(t),
	}}
	starter := &stubSessionStarter{
		onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
			t.Fatal("ResumeSession should not use request cwd when stored cwd is missing")
			return nil, nil
		},
	}
	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, starter, nil, nil, nil).(*service)

	_, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-1", CWD: "/repo/current-window"})
	if err == nil {
		t.Fatal("Resume() error = nil, want missing authoritative cwd error")
	}
	if !strings.Contains(err.Error(), "cwd is required") {
		t.Fatalf("Resume() error = %v, want cwd required", err)
	}
}

func newResumeCodexDisabledNativeToolsService(
	t *testing.T,
	runtime map[string]any,
	onResume func(context.Context, dto.ResumeSessionRequest) (contract.Session, error),
) *service {
	t.Helper()

	const providerThreadID = "11111111-2222-3333-4444-555555555552"
	rolloutPath := writeExistingProviderHistoryFile(t)
	snapshot := validThreadPromptSnapshotForTest("resume")
	threads := &stubThreadStore{
		thread: &threadstore.Thread{
			ThreadID:       "thread-native-tools",
			AgentID:        "agent-native-tools",
			Prompt:         "resume",
			Model:          "stored-model",
			Cwd:            "/repo",
			CreatedAt:      123,
			Status:         statusCreated,
			ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Runtime: runtime}),
		},
		promptSnapshot: &snapshot,
	}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-native-tools",
		Provider:         "codex",
		ProviderThreadID: providerThreadID,
		CodexThreadID:    "thread-native-tools",
		RolloutPath:      rolloutPath,
		Cwd:              "/repo",
	}}
	return NewService(
		silentLogger(),
		threads,
		bindings,
		&stubSessionProvider{},
		&stubSessionStarter{onResume: onResume},
		nil,
		&stubThreadOrchestration{},
		nil,
	).(*service)
}

func assertResumeRebuildRequest(t *testing.T, req dto.ResumeSessionRequest, providerThreadID string) {
	t.Helper()
	if req.Provider != "codex" {
		t.Fatalf("Provider = %q, want codex", req.Provider)
	}
	if req.AgentID != "agent-1" {
		t.Fatalf("AgentID = %q, want agent-1", req.AgentID)
	}
	if req.ThreadID != "thread-1" {
		t.Fatalf("ThreadID = %q, want thread-1", req.ThreadID)
	}
	if req.ProviderThreadID != providerThreadID {
		t.Fatalf("ProviderThreadID = %q, want %s", req.ProviderThreadID, providerThreadID)
	}
	if req.Model != "override-model" {
		t.Fatalf("Model = %q, want override-model", req.Model)
	}
	wantConfig := map[string]any{"additionalWorkingDirectories": []any{"/repo/extra"}}
	if !reflect.DeepEqual(req.Config, wantConfig) {
		t.Fatalf("Config = %#v, want %#v", req.Config, wantConfig)
	}
}

func assertResumeRebuildResult(t *testing.T, result ResumeResult, providerThreadID string) {
	t.Helper()
	if result.ThreadID != "thread-1" {
		t.Fatalf("ThreadID = %q, want thread-1", result.ThreadID)
	}
	if result.SessionID != providerThreadID {
		t.Fatalf("SessionID = %q, want %s", result.SessionID, providerThreadID)
	}
	if result.Status != "resumed" {
		t.Fatalf("Status = %q, want resumed", result.Status)
	}
	if result.Model != "override-model" {
		t.Fatalf("Model = %q, want override-model", result.Model)
	}
	if result.CWD != "/repo" {
		t.Fatalf("CWD = %q, want /repo", result.CWD)
	}
}

func assertResumeRebuildSideEffects(
	t *testing.T,
	sessions *stubSessionProvider,
	threads *stubThreadStore,
	bindings *stubBindingStore,
	orch *stubThreadOrchestration,
) {
	t.Helper()
	if len(sessions.removed) != 1 || sessions.removed[0] != "agent-1" {
		t.Fatalf("removed sessions = %#v, want [agent-1]", sessions.removed)
	}
	if threads.upsert.ThreadID != "thread-1" {
		t.Fatalf("persisted thread id = %q, want thread-1", threads.upsert.ThreadID)
	}
	if bindings.upsert.AgentID != "" {
		t.Fatalf("binding upsert = %#v, want idempotent no-op", bindings.upsert)
	}
	if orch.launchReq.Cwd != "/repo" {
		t.Fatalf("launch cwd = %q, want /repo", orch.launchReq.Cwd)
	}
	if orch.launchReq.Name != "resume" {
		t.Fatalf("launch name = %q, want resume", orch.launchReq.Name)
	}
	if threads.upsert.Prompt != "resume" {
		t.Fatalf("persisted prompt = %q, want resume", threads.upsert.Prompt)
	}
	if !reflect.DeepEqual(orch.launchReq.Env, []string{"AGENT_PROVIDER=codex", "AGENT_MODEL=override-model"}) {
		t.Fatalf("launch env = %#v", orch.launchReq.Env)
	}
}

func TestServiceResumeDropsDefaultPlaceholderName(t *testing.T) {
	t.Parallel()

	const providerThreadID = "11111111-2222-3333-4444-555555555556"
	const agentID = "agent-placeholder-name"
	rolloutPath := writeExistingProviderHistoryFile(t)
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:        agentID,
		AgentID:         agentID,
		Name:            defaultThreadName(),
		Prompt:          defaultThreadName(),
		Model:           "gpt-5.5",
		Cwd:             "/repo",
		CreatedAt:       123,
		Status:          statusCreated,
		ManuallyRenamed: false,
		ConfigOverride:  legacyPromptSnapshotMigrationConfig(t),
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          agentID,
		Provider:         "codex",
		ProviderThreadID: providerThreadID,
		CodexThreadID:    agentID,
		RolloutPath:      rolloutPath,
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.ProviderThreadID != providerThreadID {
				t.Fatalf("ProviderThreadID = %q, want %s", req.ProviderThreadID, providerThreadID)
			}
			session := &stubSession{threadID: providerThreadID, rolloutPath: rolloutPath}
			sessions.session = session
			return session, nil
		},
	}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)
	var startedEvents []threaddto.Started
	svc.emitStarted = func(ev threaddto.Started) {
		startedEvents = append(startedEvents, ev)
	}

	result, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: agentID})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if result.ThreadID != agentID || result.SessionID != providerThreadID {
		t.Fatalf("Resume() result = %#v", result)
	}
	if orch.launchReq.Name != "" {
		t.Fatalf("launch name = %q, want empty", orch.launchReq.Name)
	}
	if threads.upsert.Name != "" || threads.upsert.Prompt != "" {
		t.Fatalf("persisted name/prompt = %q/%q, want empty", threads.upsert.Name, threads.upsert.Prompt)
	}
	if len(startedEvents) != 1 {
		t.Fatalf("thread.Started events = %d, want 1", len(startedEvents))
	}
	if startedEvents[0].Name != "" {
		t.Fatalf("thread.Started name = %q, want empty", startedEvents[0].Name)
	}
}

func TestServiceResumeBackfillsDefaultCodexIdentityWhenPackagedRuntime(t *testing.T) {
	codexHome := t.TempDir()
	wantCodexHome := canonicalCodexHomeForTest(t, codexHome)
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	const providerThreadID = "11111111-2222-3333-4444-555555555552"
	rolloutPath := writeExistingProviderHistoryFile(t)
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:       "thread-1",
		AgentID:        "agent-1",
		Prompt:         "resume",
		Model:          "stored-model",
		Cwd:            "/repo",
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: providerThreadID,
		CodexThreadID:    "thread-1",
		RolloutPath:      rolloutPath,
		Cwd:              "/repo",
		CodexHome:        codexHome,
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.CodexHome != wantCodexHome ||
				req.CodexInstanceKey != defaultCodexInstanceKey ||
				req.CodexModelProvider != defaultCodexModelProvider {
				t.Fatalf("resume codex identity = (%q,%q,%q), want (%q,%q,%q)",
					req.CodexHome,
					req.CodexInstanceKey,
					req.CodexModelProvider,
					wantCodexHome,
					defaultCodexInstanceKey,
					defaultCodexModelProvider)
			}
			session := &stubSession{threadID: providerThreadID, rolloutPath: rolloutPath}
			sessions.session = session
			return session, nil
		},
	}

	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, &stubThreadOrchestration{}, nil).(*service)
	_, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-1"})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if bindings.upsert.CodexHome != wantCodexHome ||
		bindings.upsert.CodexInstanceKey != defaultCodexInstanceKey ||
		bindings.upsert.CodexModelProvider != defaultCodexModelProvider {
		t.Fatalf("persisted codex identity = (%q,%q,%q), want (%q,%q,%q)",
			bindings.upsert.CodexHome,
			bindings.upsert.CodexInstanceKey,
			bindings.upsert.CodexModelProvider,
			wantCodexHome,
			defaultCodexInstanceKey,
			defaultCodexModelProvider)
	}
}

func TestServiceResumePrefersStoredPromptSnapshot(t *testing.T) {
	t.Parallel()

	const providerThreadID = "11111111-2222-3333-4444-555555555553"
	rolloutPath := writeExistingProviderHistoryFile(t)
	stored := threadstore.PromptSnapshot{
		DisplayName:           "resume",
		BaseInstructions:      "stored base",
		DeveloperInstructions: "stored dev",
		Provider:              "codex",
		Version:               contract.PromptAssemblySnapshotVersion,
		Hash:                  promptSnapshotHash("resume", "stored base", "stored dev", "codex", nil, nil, 0),
	}
	threads := &stubThreadStore{
		thread: &threadstore.Thread{
			ThreadID:      "thread-1",
			AgentID:       "agent-1",
			Prompt:        "resume",
			Model:         "stored-model",
			Cwd:           "/repo",
			CreatedAt:     123,
			Status:        statusCreated,
			LastEventType: "",
		},
		promptSnapshot: &stored,
	}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: providerThreadID,
		CodexThreadID:    "thread-1",
		RolloutPath:      rolloutPath,
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.PromptSnapshot.BaseInstructions != stored.BaseInstructions || req.PromptSnapshot.DeveloperInstructions != stored.DeveloperInstructions {
				t.Fatalf("PromptSnapshot = %#v, want stored snapshot", req.PromptSnapshot)
			}
			session := &stubSession{threadID: providerThreadID, rolloutPath: rolloutPath}
			sessions.session = session
			return session, nil
		},
	}

	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, &stubThreadOrchestration{}, nil).(*service)
	_, err := svc.Resume(context.Background(), ResumeRequest{
		ThreadID: "thread-1",
		PromptSnapshot: contract.PromptAssemblySnapshot{
			DisplayName:           "caller",
			BaseInstructions:      "caller base",
			DeveloperInstructions: "caller dev",
			Provider:              "codex",
		},
	})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
}

func TestServiceResumeRehydratesClaudeOverrideConfig(t *testing.T) {
	t.Parallel()

	model := "claude-sonnet-4-20250514[1m]"
	effort := "max"
	const providerThreadID = "11111111-2222-3333-4444-555555555554"
	rolloutPath := writeExistingProviderHistoryFile(t)
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-1",
		AgentID:   "agent-1",
		Prompt:    "resume",
		Model:     "sonnet",
		Cwd:       "/repo",
		CreatedAt: 123,
		Status:    statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{
			Model:  model,
			Effort: effort,
			Runtime: map[string]any{
				"legacyPromptSnapshotMigration": true,
			},
		}),
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "claude",
		ProviderThreadID: providerThreadID,
		CodexThreadID:    "thread-1",
		RolloutPath:      rolloutPath,
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			assertClaudeOverrideResumeRequest(t, req, model, effort)
			session := &stubSession{threadID: providerThreadID, rolloutPath: rolloutPath}
			sessions.session = session
			return session, nil
		},
	}

	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)
	result, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-1"})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if result.Model != model {
		t.Fatalf("result.Model = %q, want %q", result.Model, model)
	}
	if !reflect.DeepEqual(orch.launchReq.Env, []string{"AGENT_PROVIDER=claude", "AGENT_MODEL=" + model}) {
		t.Fatalf("launch env = %#v", orch.launchReq.Env)
	}
}

func TestServiceResumeRehydratesClaudeHomeFromStoredRuntime(t *testing.T) {
	t.Parallel()

	const providerThreadID = "11111111-2222-3333-4444-555555555557"
	claudeHome := t.TempDir()
	rolloutPath := writeExistingProviderHistoryFile(t)
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-claude-home",
		AgentID:   "agent-claude-home",
		Prompt:    "resume",
		Model:     "sonnet",
		Cwd:       "/repo",
		CreatedAt: 123,
		Status:    statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Runtime: map[string]any{
			"claude_home":                   claudeHome,
			"legacyPromptSnapshotMigration": true,
		}}),
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-claude-home",
		Provider:         "claude",
		ProviderThreadID: providerThreadID,
		CodexThreadID:    "thread-claude-home",
		RolloutPath:      rolloutPath,
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.ClaudeHome != claudeHome {
				t.Fatalf("ClaudeHome = %q, want %q", req.ClaudeHome, claudeHome)
			}
			session := &stubSession{threadID: providerThreadID, rolloutPath: rolloutPath}
			sessions.session = session
			return session, nil
		},
	}

	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, &stubThreadOrchestration{}, nil).(*service)
	if _, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-claude-home"}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
}

func TestServiceResumeRequestClaudeHomeOverridesStoredRuntime(t *testing.T) {
	t.Parallel()

	const providerThreadID = "11111111-2222-3333-4444-555555555558"
	storedHome := t.TempDir()
	requestHome := t.TempDir()
	rolloutPath := writeExistingProviderHistoryFile(t)
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-claude-home-override",
		AgentID:   "agent-claude-home-override",
		Prompt:    "resume",
		Model:     "sonnet",
		Cwd:       "/repo",
		CreatedAt: 123,
		Status:    statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Runtime: map[string]any{
			"claudeHome":                    storedHome,
			"legacyPromptSnapshotMigration": true,
		}}),
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-claude-home-override",
		Provider:         "claude",
		ProviderThreadID: providerThreadID,
		CodexThreadID:    "thread-claude-home-override",
		RolloutPath:      rolloutPath,
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.ClaudeHome != requestHome {
				t.Fatalf("ClaudeHome = %q, want request override %q", req.ClaudeHome, requestHome)
			}
			session := &stubSession{threadID: providerThreadID, rolloutPath: rolloutPath}
			sessions.session = session
			return session, nil
		},
	}

	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, &stubThreadOrchestration{}, nil).(*service)
	if _, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-claude-home-override", ClaudeHome: requestHome}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
}

func assertClaudeOverrideResumeRequest(t *testing.T, req dto.ResumeSessionRequest, model, effort string) {
	t.Helper()
	if req.Provider != "claude" {
		t.Fatalf("Provider = %q, want claude", req.Provider)
	}
	if req.Model != model {
		t.Fatalf("Model = %q, want %q", req.Model, model)
	}
	if req.Effort != effort {
		t.Fatalf("Effort = %q, want %q", req.Effort, effort)
	}
	assertClaudeOverrideConfig(t, req.ConfigOverride, model, effort)
}

func assertClaudeOverrideConfig(t *testing.T, config dto.ThreadConfigPatch, model, effort string) {
	t.Helper()
	if config.Model == nil || *config.Model != model {
		t.Fatalf("ConfigOverride.Model = %#v, want %q", config.Model, model)
	}
	if config.Effort == nil || *config.Effort != effort {
		t.Fatalf("ConfigOverride.Effort = %#v, want %q", config.Effort, effort)
	}
}
