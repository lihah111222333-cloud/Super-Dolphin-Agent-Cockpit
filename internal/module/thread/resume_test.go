package thread

import (
	"context"
	"reflect"
	"testing"
	"time"

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
	threads := &stubThreadStore{thread: &threadstore.Thread{
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
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:            "agent-1",
		Provider:           "codex",
		ProviderThreadID:   providerThreadID,
		CodexThreadID:      "thread-1",
		RolloutPath:        rolloutPath,
		Cwd:                "/repo",
		CodexHome:          "/repo/.codex",
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
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

func TestResumeSessionDoesNotSynthesizeProcessCWDForDot(t *testing.T) {
	t.Parallel()

	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		if req.CWD != "" {
			t.Fatalf("ResumeSession CWD = %q, want empty for untrusted dot cwd", req.CWD)
		}
		return &stubSession{threadID: "provider-thread-1"}, nil
	}}
	svc := &service{starter: starter}
	if _, err := svc.resumeSession(context.Background(), ResumeRequest{
		Provider: "codex",
		AgentID:  "agent-dot",
		ThreadID: "thread-dot",
		CWD:      ".",
	}); err != nil {
		t.Fatalf("resumeSession() error = %v", err)
	}
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

func TestServiceResumeBackfillsDefaultCodexIdentityWhenOptedIn(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv(legacyDefaultCodexHomeEnvVar, legacyDefaultCodexHomeEnabled)
	const providerThreadID = "11111111-2222-3333-4444-555555555552"
	rolloutPath := writeExistingProviderHistoryFile(t)
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-1",
		AgentID:   "agent-1",
		Prompt:    "resume",
		Model:     "stored-model",
		Cwd:       "/repo",
		CreatedAt: 123,
		Status:    statusCreated,
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
			if req.CodexHome != codexHome ||
				req.CodexInstanceKey != defaultCodexInstanceKey ||
				req.CodexModelProvider != defaultCodexModelProvider {
				t.Fatalf("resume codex identity = (%q,%q,%q), want (%q,%q,%q)",
					req.CodexHome,
					req.CodexInstanceKey,
					req.CodexModelProvider,
					codexHome,
					defaultCodexInstanceKey,
					defaultCodexModelProvider)
			}
			session := &stubSession{threadID: providerThreadID, rolloutPath: rolloutPath}
			sessions.session = session
			return session, nil
		},
	}

	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, nil, nil).(*service)
	_, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-1"})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if bindings.upsert.CodexHome != codexHome ||
		bindings.upsert.CodexInstanceKey != defaultCodexInstanceKey ||
		bindings.upsert.CodexModelProvider != defaultCodexModelProvider {
		t.Fatalf("persisted codex identity = (%q,%q,%q), want (%q,%q,%q)",
			bindings.upsert.CodexHome,
			bindings.upsert.CodexInstanceKey,
			bindings.upsert.CodexModelProvider,
			codexHome,
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
		Hash:                  promptSnapshotHash("resume", "stored base", "stored dev", "codex", nil),
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

	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, nil, nil).(*service)
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
		ThreadID:       "thread-1",
		AgentID:        "agent-1",
		Prompt:         "resume",
		Model:          "sonnet",
		Cwd:            "/repo",
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Model: model, Effort: effort}),
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

func TestServiceResumeClaudeWithoutStoredOverrideDoesNotInventConfigOverride(t *testing.T) {
	t.Parallel()

	const providerThreadID = "11111111-2222-3333-4444-555555555555"
	rolloutPath := writeExistingProviderHistoryFile(t)
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:      "thread-1",
		AgentID:       "agent-1",
		Prompt:        "resume",
		Model:         "sonnet",
		Cwd:           "/repo",
		CreatedAt:     123,
		Status:        statusCreated,
		LastEventType: "",
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
			if req.Model != "sonnet" {
				t.Fatalf("Model = %q, want sonnet", req.Model)
			}
			if req.Effort != "" {
				t.Fatalf("Effort = %q, want empty", req.Effort)
			}
			if req.ConfigOverride.Model != nil || req.ConfigOverride.Effort != nil {
				t.Fatalf("ConfigOverride = %#v, want empty", req.ConfigOverride)
			}
			session := &stubSession{threadID: providerThreadID, rolloutPath: rolloutPath}
			sessions.session = session
			return session, nil
		},
	}

	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)
	if _, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-1"}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if !reflect.DeepEqual(orch.launchReq.Env, []string{"AGENT_PROVIDER=claude", "AGENT_MODEL=sonnet"}) {
		t.Fatalf("launch env = %#v", orch.launchReq.Env)
	}
}

func TestBackgroundResumeIfNeededRehydratesClaudeOverrideConfig(t *testing.T) {
	t.Parallel()

	model := "claude-sonnet-4-20250514[1m]"
	effort := "max"
	const providerThreadID = "11111111-2222-3333-4444-555555555555"
	rolloutPath := writeExistingProviderHistoryFile(t)
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:       "thread-1",
		AgentID:        "agent-1",
		Prompt:         "resume",
		Model:          "sonnet",
		Cwd:            "/repo",
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Model: model, Effort: effort}),
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
	resumeReqCh := make(chan dto.ResumeSessionRequest, 1)
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			select {
			case resumeReqCh <- req:
			default:
			}
			session := &stubSession{threadID: providerThreadID, rolloutPath: rolloutPath}
			sessions.session = session
			return session, nil
		},
	}

	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, nil, nil).(*service)
	svc.backgroundResumeIfNeeded(context.Background(), "thread-1")

	select {
	case req := <-resumeReqCh:
		assertBackgroundClaudeOverrideResumeRequest(t, req, providerThreadID, model, effort)
	case <-time.After(time.Second):
		t.Fatal("backgroundResumeIfNeeded() did not trigger resume")
	}
}

func assertBackgroundClaudeOverrideResumeRequest(t *testing.T, req dto.ResumeSessionRequest, providerThreadID, model, effort string) {
	t.Helper()
	if req.ProviderThreadID != providerThreadID {
		t.Fatalf("ProviderThreadID = %q, want %s", req.ProviderThreadID, providerThreadID)
	}
	if req.Model != model || req.Effort != effort {
		t.Fatalf("ResumeSession request = %#v, want model/effort restored", req)
	}
	assertClaudeOverrideConfig(t, req.ConfigOverride, model, effort)
}
