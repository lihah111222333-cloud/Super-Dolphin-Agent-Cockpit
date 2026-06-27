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

func TestHandoff_RejectsEmptySourceThreadID(t *testing.T) {
	t.Parallel()
	s := &service{}
	_, err := s.Handoff(context.Background(), HandoffRequest{
		SourceThreadID: "   ",
		TargetAgentKey: "sql_expert",
	})
	if !errors.Is(err, errHandoffMissingSource) {
		t.Fatalf("want errHandoffMissingSource, got %v", err)
	}
}

func TestHandoff_RejectsEmptyAgentKey(t *testing.T) {
	t.Parallel()
	s := &service{}
	_, err := s.Handoff(context.Background(), HandoffRequest{
		SourceThreadID: "thread-123",
		TargetAgentKey: "",
	})
	if !errors.Is(err, errHandoffMissingAgentKey) {
		t.Fatalf("want errHandoffMissingAgentKey, got %v", err)
	}
}

func TestHandoff_NilStoreErrors(t *testing.T) {
	t.Parallel()
	s := &service{} // no threadStore set
	_, err := s.Handoff(context.Background(), HandoffRequest{
		SourceThreadID: "thread-123",
		TargetAgentKey: "sql_expert",
	})
	if err == nil {
		t.Fatalf("expected error when threadStore is nil")
	}
}

func TestHandoff_SourceNotFound(t *testing.T) {
	t.Parallel()
	s := &service{threadStore: &stubThreadStore{}}
	_, err := s.Handoff(context.Background(), HandoffRequest{
		SourceThreadID: "missing",
		TargetAgentKey: "sql_expert",
	})
	if err == nil {
		t.Fatalf("expected not-found error")
	}
}

func TestHandoff_StoreError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("pgx: boom")
	s := &service{threadStore: &stubThreadStore{getErr: sentinel}}
	_, err := s.Handoff(context.Background(), HandoffRequest{
		SourceThreadID: "thread-123",
		TargetAgentKey: "sql_expert",
	})
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("expected wrapped sentinel error, got %v", err)
	}
}

func TestHandoff_LoadsNarrowSourceFields(t *testing.T) {
	t.Parallel()
	row := &threadstore.Thread{
		ThreadID:      "thread-src",
		AgentID:       "agent-src",
		Cwd:           "/work/repo",
		Model:         "claude-sonnet-4",
		AgentType:     "main",
		ParentAgentID: "parent-agent",
	}
	s := &service{
		threadStore: &stubThreadStore{thread: row},
		bindingStore: &stubBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-src",
			Provider:         "claude",
			ProviderThreadID: "thread-src",
			CodexThreadID:    "thread-src",
		}},
	}
	src, _, err := s.loadThreadForHandoff(context.Background(), "thread-src")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src.Cwd != row.Cwd || src.Model != row.Model || src.AgentType != row.AgentType ||
		src.ParentAgentID != row.ParentAgentID || src.ThreadID != row.ThreadID || src.Provider != "claude" {
		t.Fatalf("narrow view drift: got %+v", src)
	}
}

func TestHandoff_UsesSourceBindingProvider(t *testing.T) {
	t.Parallel()

	rolloutPath := writeExistingProviderHistoryFile(t)
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
			if req.Provider != "codex" {
				t.Fatalf("provider = %q, want codex from source binding", req.Provider)
			}
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f5b3", rolloutPath: rolloutPath}
			sessions.session = session
			return session, nil
		},
	}
	source := &threadstore.Thread{
		ThreadID: "thread-src",
		AgentID:  "agent-src",
		Cwd:      wantStartCWD(t),
		Model:    "gpt-5.5",
		Prompt:   "Source task",
	}
	svc := NewService(
		silentLogger(),
		&stubThreadStore{thread: source},
		&stubBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-src",
			Provider:         "codex",
			ProviderThreadID: "thread-src",
			CodexThreadID:    "thread-src",
		}},
		sessions,
		starter,
		nil,
		&stubThreadOrchestration{},
		nil,
	).(*service)

	result, err := svc.Handoff(context.Background(), HandoffRequest{
		SourceThreadID: "thread-src",
		TargetAgentKey: "reviewer",
		InitialMessage: "review this",
	})
	if err != nil {
		t.Fatalf("Handoff() error = %v", err)
	}
	if result.SourceThreadID != "thread-src" || result.AgentKey != "reviewer" || result.NewThreadID == "" {
		t.Fatalf("Handoff() result = %#v", result)
	}
}

func TestHandoff_PreservesSourceCodexIdentity(t *testing.T) {
	t.Parallel()

	const (
		instanceKey   = "qwen"
		modelProvider = "openai-compatible-qwen"
	)
	codexHome := t.TempDir()
	wantCodexHome := canonicalCodexHomeForTest(t, codexHome)
	rolloutPath := writeExistingProviderHistoryFile(t)
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
			if req.Config[contract.CodexHomeKey] != wantCodexHome ||
				req.Config[contract.CodexInstanceKeyKey] != instanceKey ||
				req.Config[contract.CodexModelProviderKey] != modelProvider {
				t.Fatalf("handoff start config identity = %#v, want source identity", req.Config)
			}
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f5b3", rolloutPath: rolloutPath}
			sessions.session = session
			return session, nil
		},
	}
	source := &threadstore.Thread{
		ThreadID: "thread-src",
		AgentID:  "agent-src",
		Cwd:      wantStartCWD(t),
		Model:    "gpt-5.5",
		Prompt:   "Source task",
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{
			Runtime: map[string]any{
				contract.CodexHomeKey:          codexHome,
				contract.CodexInstanceKeyKey:   instanceKey,
				contract.CodexModelProviderKey: modelProvider,
			},
		}),
	}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:            "agent-src",
		Provider:           "codex",
		ProviderThreadID:   "thread-src",
		CodexThreadID:      "thread-src",
		CodexHome:          codexHome,
		CodexInstanceKey:   instanceKey,
		CodexModelProvider: modelProvider,
		Cwd:                wantStartCWD(t),
	}}
	threads := &stubThreadStore{thread: source}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, &stubThreadOrchestration{}, nil).(*service)

	result, err := svc.Handoff(context.Background(), HandoffRequest{
		SourceThreadID: "thread-src",
		TargetAgentKey: "reviewer",
		InitialMessage: "review this",
	})
	if err != nil {
		t.Fatalf("Handoff() error = %v", err)
	}
	if result.NewThreadID == "" || result.AgentKey != "reviewer" {
		t.Fatalf("Handoff() result = %#v", result)
	}
	assertPersistedCodexIdentity(t, bindings.upsert, codexHome, instanceKey, modelProvider)
	assertStoredRuntimeCodexIdentity(t, threads.upsert.ConfigOverride, codexHome, instanceKey, modelProvider)
}

func TestHandoff_RejectsPartialSourceCodexIdentity(t *testing.T) {
	t.Parallel()

	source := &threadstore.Thread{
		ThreadID: "thread-src",
		AgentID:  "agent-src",
		Cwd:      wantStartCWD(t),
		Model:    "gpt-5.5",
	}
	starter := &startOnlySessionStarter{
		onStart: func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
			t.Fatal("StartSession should not be called when source codex identity is partial")
			return nil, nil
		},
	}
	svc := NewService(
		silentLogger(),
		&stubThreadStore{thread: source},
		&stubBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-src",
			Provider:         "codex",
			ProviderThreadID: "thread-src",
			CodexThreadID:    "thread-src",
			CodexHome:        t.TempDir(),
			Cwd:              wantStartCWD(t),
		}},
		&stubSessionProvider{},
		starter,
		nil,
		&stubThreadOrchestration{},
		nil,
	).(*service)

	_, err := svc.Handoff(context.Background(), HandoffRequest{
		SourceThreadID: "thread-src",
		TargetAgentKey: "reviewer",
	})
	if !errors.Is(err, contract.ErrCodexInstanceKeyRequired) {
		t.Fatalf("Handoff() error = %v, want %v", err, contract.ErrCodexInstanceKeyRequired)
	}
}
