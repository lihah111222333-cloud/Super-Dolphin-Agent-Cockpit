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

// fakeThreadStoreForHandoff only implements the method Handoff's loader uses.
// All other Store methods panic so an accidental call fails loudly.
type fakeThreadStoreForHandoff struct {
	row *threadstore.Thread
	err error
}

func (f *fakeThreadStoreForHandoff) GetByThreadID(_ context.Context, _ string) (*threadstore.Thread, error) {
	return f.row, f.err
}

// --- unused methods: panic so accidental use is caught ---
func (f *fakeThreadStoreForHandoff) GetByPort(context.Context, int32) (*threadstore.Thread, error) {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) ListAll(context.Context) ([]threadstore.Thread, error) {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) ListConfigsByIDs(context.Context, []string) ([]threadstore.Thread, error) {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) ListRunning(context.Context) ([]threadstore.Thread, error) {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) ListRecoverable(context.Context) ([]threadstore.Thread, error) {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) ListRunningAgents(context.Context) ([]threadstore.RunningAgent, error) {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) Upsert(context.Context, threadstore.UpsertParams) error {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) SavePromptSnapshot(context.Context, string, threadstore.PromptSnapshot) error {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) LoadPromptSnapshot(context.Context, string) (*threadstore.PromptSnapshot, error) {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) UpdateStatus(context.Context, threadstore.UpdateStatusParams) error {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) UpdateLaunchResult(context.Context, threadstore.UpdateLaunchResultParams) error {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) DeleteByThreadID(context.Context, string) error {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) ResetRunning(context.Context) error {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) ExpireStale(context.Context, threadstore.ExpireStaleParams) (int64, error) {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) RunningExists(context.Context, string) (bool, error) {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) ListCwds(context.Context) ([]threadstore.ThreadCwd, error) {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) ListCwdsByPrefix(context.Context, string) ([]threadstore.ThreadCwd, error) {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) CountChildren(context.Context, string) (int64, error) {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) Exists(context.Context, string) (bool, error) {
	panic("unused")
}
func (f *fakeThreadStoreForHandoff) CountAll(context.Context) (int64, error) {
	panic("unused")
}

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
	s := &service{threadStore: &fakeThreadStoreForHandoff{row: nil, err: nil}}
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
	s := &service{threadStore: &fakeThreadStoreForHandoff{err: sentinel}}
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
		threadStore: &fakeThreadStoreForHandoff{row: row},
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
