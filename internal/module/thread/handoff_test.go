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
	src, err := s.loadThreadForHandoff(context.Background(), "thread-src")
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
