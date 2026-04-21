package thread

import (
	"context"
	"errors"
	"testing"

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
		Cwd:           "/work/repo",
		Model:         "claude-sonnet-4",
		AgentType:     "main",
		ParentAgentID: "parent-agent",
	}
	s := &service{threadStore: &fakeThreadStoreForHandoff{row: row}}
	src, err := s.loadThreadForHandoff(context.Background(), "thread-src")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src.Cwd != row.Cwd || src.Model != row.Model || src.AgentType != row.AgentType ||
		src.ParentAgentID != row.ParentAgentID || src.ThreadID != row.ThreadID {
		t.Fatalf("narrow view drift: got %+v", src)
	}
}
