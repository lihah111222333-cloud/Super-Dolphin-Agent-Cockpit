package thread

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestServiceForkCreatesIndependentAgentAndBinding(t *testing.T) {
	t.Parallel()

	originalSession := &stubSession{
		threadID:   "thread-parent",
		forkResult: dto.ForkResult{NewThreadID: "thread-fork"},
	}
	forkedSession := &stubSession{threadID: "thread-fork"}
	sessions := &stubSessionProvider{session: originalSession}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-parent",
		Provider:         "codex",
		ProviderThreadID: "thread-parent",
		CodexThreadID:    "thread-parent",
		Cwd:              "/repo",
	}}
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-parent",
		Prompt:    "Forked Thread",
		Model:     "gpt-5.4",
		Cwd:       "/repo",
		CreatedAt: 123,
	}}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.Provider != "codex" {
				t.Fatalf("Provider = %q, want codex", req.Provider)
			}
			if req.AgentID != "thread-fork" {
				t.Fatalf("AgentID = %q, want thread-fork", req.AgentID)
			}
			if req.ThreadID != "thread-fork" {
				t.Fatalf("ThreadID = %q, want thread-fork", req.ThreadID)
			}
			if req.Model != "gpt-5.4" {
				t.Fatalf("Model = %q, want gpt-5.4", req.Model)
			}
			sessions.session = forkedSession
			return forkedSession, nil
		},
	}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	result, err := svc.Fork(context.Background(), "thread-parent")
	if err != nil {
		t.Fatalf("Fork() error = %v", err)
	}
	if result.NewThreadID != "thread-fork" || result.ForkedFrom != "thread-parent" {
		t.Fatalf("Fork() result = %#v, want thread-fork", result)
	}
	if originalSession.forkRequest.ThreadID != "thread-parent" {
		t.Fatalf("forkRequest.ThreadID = %q, want thread-parent", originalSession.forkRequest.ThreadID)
	}
	if orch.launch.AgentID != "thread-fork" {
		t.Fatalf("launch.AgentID = %q, want thread-fork", orch.launch.AgentID)
	}
	if orch.launch.Cwd != "/repo" || orch.launch.Name != "Forked Thread" {
		t.Fatalf("launch = %#v", orch.launch)
	}
	if bindings.upsert.AgentID != "thread-fork" {
		t.Fatalf("binding.AgentID = %q, want thread-fork", bindings.upsert.AgentID)
	}
	if bindings.upsert.ProviderThreadID != "thread-fork" || bindings.upsert.CodexThreadID != "thread-fork" {
		t.Fatalf("binding upsert = %#v", bindings.upsert)
	}
	if threads.upsert.ThreadID != "thread-fork" || threads.upsert.OwnerThreadID != "thread-parent" {
		t.Fatalf("thread upsert = %#v", threads.upsert)
	}
	if threads.upsert.Prompt != "Forked Thread" {
		t.Fatalf("persisted prompt = %q, want Forked Thread", threads.upsert.Prompt)
	}
}

func TestServiceRecoverReturnsResumeEnvelopeWhenSessionMissing(t *testing.T) {
	t.Parallel()

	resumedSession := &stubSession{threadID: "provider-parent"}
	sessions := &stubSessionProvider{}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-parent",
		Provider:         "codex",
		ProviderThreadID: "provider-parent",
		CodexThreadID:    "thread-parent",
		Cwd:              "/repo",
	}}
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-parent",
		AgentID:   "agent-parent",
		Prompt:    "Recovered Thread",
		Model:     "gpt-5.4",
		Cwd:       "/repo",
		CreatedAt: 123,
	}}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.Provider != "codex" || req.AgentID != "agent-parent" || req.ThreadID != "thread-parent" {
				t.Fatalf("ResumeSession request = %#v", req)
			}
			if req.ProviderThreadID != "provider-parent" {
				t.Fatalf("ProviderThreadID = %q, want provider-parent", req.ProviderThreadID)
			}
			sessions.session = resumedSession
			return resumedSession, nil
		},
	}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	result, err := svc.Recover(context.Background(), "thread-parent")
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if result != (RecoverResult{ThreadID: "thread-parent", Status: "recovering", Recovered: true, Mode: "relaunch_resume"}) {
		t.Fatalf("Recover() result = %#v", result)
	}
	if len(orch.recovered) != 1 || orch.recovered[0] != "agent-parent" {
		t.Fatalf("recover calls = %#v", orch.recovered)
	}
	if len(orch.bindAgentIDs) != 0 {
		t.Fatalf("bind session calls = %#v, want none without session generation support", orch.bindAgentIDs)
	}
	if threads.upsert.ThreadID != "thread-parent" {
		t.Fatalf("thread upsert = %#v", threads.upsert)
	}
	if threads.upsert.Prompt != "Recovered Thread" {
		t.Fatalf("persisted prompt = %q, want Recovered Thread", threads.upsert.Prompt)
	}
}

func TestServiceRecoverRehydratesClaudeOverrideConfigWhenSessionMissing(t *testing.T) {
	t.Parallel()

	model := "claude-sonnet-4-20250514[1m]"
	effort := "max"
	resumedSession := &stubSession{threadID: "provider-parent"}
	sessions := &stubSessionProvider{}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-parent",
		Provider:         "claude",
		ProviderThreadID: "provider-parent",
		CodexThreadID:    "thread-parent",
		Cwd:              "/repo",
	}}
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:       "thread-parent",
		AgentID:        "agent-parent",
		Prompt:         "Recovered Thread",
		Model:          "sonnet",
		Cwd:            "/repo",
		CreatedAt:      123,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Model: model, Effort: effort}),
	}}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.Provider != "claude" || req.AgentID != "agent-parent" || req.ThreadID != "thread-parent" {
				t.Fatalf("ResumeSession request = %#v", req)
			}
			if req.Model != model || req.Effort != effort {
				t.Fatalf("ResumeSession request = %#v, want model/effort restored", req)
			}
			if req.ConfigOverride.Model == nil || *req.ConfigOverride.Model != model {
				t.Fatalf("ConfigOverride.Model = %#v, want %q", req.ConfigOverride.Model, model)
			}
			if req.ConfigOverride.Effort == nil || *req.ConfigOverride.Effort != effort {
				t.Fatalf("ConfigOverride.Effort = %#v, want %q", req.ConfigOverride.Effort, effort)
			}
			sessions.session = resumedSession
			return resumedSession, nil
		},
	}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	result, err := svc.Recover(context.Background(), "thread-parent")
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if result != (RecoverResult{ThreadID: "thread-parent", Status: "recovering", Recovered: true, Mode: "relaunch_resume"}) {
		t.Fatalf("Recover() result = %#v", result)
	}
}

func TestServiceRecoverReturnsRestoreEnvelopeWhenSessionActive(t *testing.T) {
	t.Parallel()

	sessions := &stubSessionProvider{session: &stubSession{threadID: "provider-parent"}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-parent",
		Provider:         "codex",
		ProviderThreadID: "provider-parent",
		CodexThreadID:    "thread-parent",
		Cwd:              "/repo",
	}}
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-parent",
		AgentID:   "agent-parent",
		Prompt:    "Recovered Thread",
		Model:     "gpt-5.4",
		Cwd:       "/repo",
		CreatedAt: 123,
	}}
	starter := &stubSessionStarter{
		onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
			t.Fatal("ResumeSession should not be called when session is already active")
			return nil, nil
		},
	}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	result, err := svc.Recover(context.Background(), "thread-parent")
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if result != (RecoverResult{ThreadID: "thread-parent", Status: "recovering", Recovered: true, Mode: "restore_launch"}) {
		t.Fatalf("Recover() result = %#v", result)
	}
	if len(orch.recovered) != 1 || orch.recovered[0] != "agent-parent" {
		t.Fatalf("recover calls = %#v", orch.recovered)
	}
	if len(orch.bindAgentIDs) != 0 {
		t.Fatalf("bind session calls = %#v, want none", orch.bindAgentIDs)
	}
}

func TestServiceRecoverInvalidatesPromptAssemblyAfterSuccess(t *testing.T) {
	t.Parallel()

	promptAssembly := &stubPromptAssemblyService{}
	sessions := &stubSessionProvider{session: &stubSession{threadID: "provider-parent"}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-parent",
		Provider:         "codex",
		ProviderThreadID: "provider-parent",
		CodexThreadID:    "thread-parent",
		Cwd:              "/repo",
	}}
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-parent",
		AgentID:   "agent-parent",
		Prompt:    "Recovered Thread",
		Model:     "gpt-5.4",
		Cwd:       "/repo",
		CreatedAt: 123,
	}}
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		t.Fatal("ResumeSession should not be called when session is already active")
		return nil, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewServiceWithPromptAssembly(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil, promptAssembly, nil, nil).(*service)

	if _, err := svc.Recover(context.Background(), "thread-parent"); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if got := promptAssembly.invalidated; len(got) != 1 || got[0] != contract.InvalidateResumeRestore {
		t.Fatalf("Invalidate calls = %#v, want [%q]", got, contract.InvalidateResumeRestore)
	}
}

type forkOrchestrationStub struct {
	launch       LaunchAgentRequest
	recovered    []string
	bindAgentIDs []string
}

func (s *forkOrchestrationStub) LaunchAgent(_ context.Context, req LaunchAgentRequest) error {
	s.launch = req
	return nil
}

func (s *forkOrchestrationStub) StopAgent(context.Context, string) error { return nil }

func (s *forkOrchestrationStub) Recover(_ context.Context, agentID string) error {
	s.recovered = append(s.recovered, agentID)
	return nil
}

func (s *forkOrchestrationStub) BindSessionGeneration(_ context.Context, agentID string, _ uint64) error {
	s.bindAgentIDs = append(s.bindAgentIDs, agentID)
	return nil
}
