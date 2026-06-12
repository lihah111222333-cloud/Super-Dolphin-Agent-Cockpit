package thread

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type threadQuerierStub struct {
	getByIDFn            func(context.Context, string) (sqlc.GetAgentThreadByIDRow, error)
	listAllFn            func(context.Context) ([]sqlc.ListAgentThreadsRow, error)
	listRecoverableFn    func(context.Context) ([]sqlc.ListRecoverableAgentThreadsRow, error)
	listRunningFn        func(context.Context) ([]sqlc.ListRunningAgentThreadsRow, error)
	loadPromptSnapshotFn func(context.Context, string) ([]byte, error)
	savePromptSnapshotFn func(context.Context, sqlc.UpdateAgentThreadPromptSnapshotParams) (int64, error)
	upsertFn             func(context.Context, sqlc.UpsertAgentThreadParams) error
}

func (s *threadQuerierStub) AgentThreadRunningExists(context.Context, sqlc.AgentThreadRunningExistsParams) (int64, error) {
	return 0, nil
}

func (s *threadQuerierStub) AgentThreadExists(context.Context, sqlc.AgentThreadExistsParams) (int64, error) {
	return 0, nil
}

func (s *threadQuerierStub) CountAllThreads(context.Context) (int64, error) {
	return 0, nil
}

func (s *threadQuerierStub) CountChildAgentThreads(context.Context, sqlc.CountChildAgentThreadsParams) (int64, error) {
	return 0, nil
}

func (s *threadQuerierStub) DeleteAgentThreadByID(context.Context, sqlc.DeleteAgentThreadByIDParams) error {
	return nil
}

func (s *threadQuerierStub) ExpireStaleAgentThreads(context.Context, sqlc.ExpireStaleAgentThreadsParams) (int64, error) {
	return 0, nil
}

func (s *threadQuerierStub) GetAgentThreadByID(ctx context.Context, arg sqlc.GetAgentThreadByIDParams) (sqlc.GetAgentThreadByIDRow, error) {
	if s.getByIDFn != nil {
		return s.getByIDFn(ctx, arg.ThreadID)
	}
	return sqlc.GetAgentThreadByIDRow{}, nil
}

func (*threadQuerierStub) GetAgentThreadByPort(context.Context, sqlc.GetAgentThreadByPortParams) (sqlc.GetAgentThreadByPortRow, error) {
	return sqlc.GetAgentThreadByPortRow{}, nil
}

func (*threadQuerierStub) ListAgentThreadConfigsByIDs(context.Context, sqlc.ListAgentThreadConfigsByIDsParams) ([]sqlc.ListAgentThreadConfigsByIDsRow, error) {
	return nil, nil
}

func (*threadQuerierStub) ListAgentThreadCwds(context.Context) ([]sqlc.ListAgentThreadCwdsRow, error) {
	return nil, nil
}

func (*threadQuerierStub) ListAgentThreadCwdsByPrefix(context.Context, sqlc.ListAgentThreadCwdsByPrefixParams) ([]sqlc.ListAgentThreadCwdsByPrefixRow, error) {
	return nil, nil
}

func (s *threadQuerierStub) ListAgentThreads(ctx context.Context) ([]sqlc.ListAgentThreadsRow, error) {
	if s.listAllFn != nil {
		return s.listAllFn(ctx)
	}
	return nil, nil
}

func (s *threadQuerierStub) ListRecoverableAgentThreads(ctx context.Context) ([]sqlc.ListRecoverableAgentThreadsRow, error) {
	if s.listRecoverableFn != nil {
		return s.listRecoverableFn(ctx)
	}
	return nil, nil
}

func (s *threadQuerierStub) ListRunningAgentThreads(ctx context.Context) ([]sqlc.ListRunningAgentThreadsRow, error) {
	if s.listRunningFn != nil {
		return s.listRunningFn(ctx)
	}
	return nil, nil
}

func (*threadQuerierStub) ListRunningAgents(context.Context) ([]sqlc.ListRunningAgentsRow, error) {
	return nil, nil
}

func (s *threadQuerierStub) LoadAgentThreadPromptSnapshot(ctx context.Context, arg sqlc.LoadAgentThreadPromptSnapshotParams) (json.RawMessage, error) {
	if s.loadPromptSnapshotFn != nil {
		return s.loadPromptSnapshotFn(ctx, arg.ThreadID)
	}
	return nil, nil
}

func (*threadQuerierStub) ResetRunningAgentThreads(context.Context) error { return nil }

func (s *threadQuerierStub) UpdateAgentThreadPromptSnapshot(ctx context.Context, arg sqlc.UpdateAgentThreadPromptSnapshotParams) (int64, error) {
	if s.savePromptSnapshotFn != nil {
		return s.savePromptSnapshotFn(ctx, arg)
	}
	return 1, nil
}

func (*threadQuerierStub) UpdateAgentThreadStatus(context.Context, sqlc.UpdateAgentThreadStatusParams) error {
	return nil
}

func (*threadQuerierStub) UpdateAgentThreadLaunchResult(context.Context, sqlc.UpdateAgentThreadLaunchResultParams) error {
	return nil
}

func (s *threadQuerierStub) UpsertAgentThread(ctx context.Context, arg sqlc.UpsertAgentThreadParams) error {
	if s.upsertFn != nil {
		return s.upsertFn(ctx, arg)
	}
	return nil
}

func TestUpsertPersistsConfigOverride(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"personality":"balanced"}`)
	var got sqlc.UpsertAgentThreadParams
	s := &store{q: &threadQuerierStub{
		upsertFn: func(_ context.Context, arg sqlc.UpsertAgentThreadParams) error {
			got = arg
			return nil
		},
	}}

	err := s.Upsert(context.Background(), UpsertParams{
		ThreadID:         "thread-1",
		Name:             "display name",
		Model:            "gpt-5.5",
		ParentAgentID:    "agent-root",
		AgentType:        "reviewer",
		AgentMemoryScope: "project",
		ConfigOverride:   raw,
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if got.ParentAgentID != "agent-root" || got.AgentType != "reviewer" || got.AgentMemoryScope != "project" {
		t.Fatalf("identity fields = %+v, want parent/type/scope forwarded", got)
	}
	if got.Name != "display name" {
		t.Fatalf("Name = %q, want display name forwarded", got.Name)
	}
	stored, ok := got.ConfigOverride.(json.RawMessage)
	if !ok {
		t.Fatalf("ConfigOverride type = %T, want json.RawMessage", got.ConfigOverride)
	}
	if string(stored) != string(raw) {
		t.Fatalf("ConfigOverride = %s, want %s", got.ConfigOverride, raw)
	}
}

func TestGetAndListMapConfigOverride(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"approvals":"never"}`)
	s := newThreadConfigOverrideStore(raw)

	thread, err := s.GetByThreadID(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("GetByThreadID() error = %v", err)
	}
	assertThreadConfigOverride(t, "GetByThreadID()", *thread, raw)

	all, err := s.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll() error = %v", err)
	}
	assertThreadListConfigOverride(t, "ListAll()", all, raw)

	running, err := s.ListRunning(context.Background())
	if err != nil {
		t.Fatalf("ListRunning() error = %v", err)
	}
	assertThreadListConfigOverride(t, "ListRunning()", running, raw)

	recoverable, err := s.ListRecoverable(context.Background())
	if err != nil {
		t.Fatalf("ListRecoverable() error = %v", err)
	}
	assertThreadListConfigOverride(t, "ListRecoverable()", recoverable, raw)
}

func newThreadConfigOverrideStore(raw []byte) *store {
	rawStr := string(raw)
	return &store{q: &threadQuerierStub{
		getByIDFn: func(context.Context, string) (sqlc.GetAgentThreadByIDRow, error) {
			return sqlc.GetAgentThreadByIDRow{ThreadID: "thread-1", Name: "display name", ParentAgentID: "agent-root", AgentType: "reviewer", AgentMemoryScope: "project", ConfigOverride: rawStr}, nil
		},
		listAllFn: func(context.Context) ([]sqlc.ListAgentThreadsRow, error) {
			return []sqlc.ListAgentThreadsRow{{ThreadID: "thread-1", Name: "display name", ParentAgentID: "agent-root", AgentType: "reviewer", AgentMemoryScope: "project", ConfigOverride: rawStr}}, nil
		},
		listRunningFn: func(context.Context) ([]sqlc.ListRunningAgentThreadsRow, error) {
			return []sqlc.ListRunningAgentThreadsRow{{ThreadID: "thread-1", Name: "display name", ParentAgentID: "agent-root", AgentType: "reviewer", AgentMemoryScope: "project", ConfigOverride: rawStr}}, nil
		},
		listRecoverableFn: func(context.Context) ([]sqlc.ListRecoverableAgentThreadsRow, error) {
			return []sqlc.ListRecoverableAgentThreadsRow{{ThreadID: "thread-1", Name: "display name", ParentAgentID: "agent-root", AgentType: "reviewer", AgentMemoryScope: "project", ConfigOverride: rawStr}}, nil
		},
	}}
}

func assertThreadListConfigOverride(t *testing.T, label string, threads []Thread, raw []byte) {
	t.Helper()
	if len(threads) != 1 {
		t.Fatalf("%s threads = %#v, want one thread", label, threads)
	}
	assertThreadConfigOverride(t, label, threads[0], raw)
}

func assertThreadConfigOverride(t *testing.T, label string, thread Thread, raw []byte) {
	t.Helper()
	if thread.ParentAgentID != "agent-root" || thread.AgentType != "reviewer" || thread.AgentMemoryScope != "project" {
		t.Fatalf("%s identity = %#v, want parent/type/scope mapped", label, thread)
	}
	if thread.Name != "display name" {
		t.Fatalf("%s Name = %q, want display name", label, thread.Name)
	}
	if string(thread.ConfigOverride) != string(raw) {
		t.Fatalf("%s ConfigOverride = %s, want %s", label, thread.ConfigOverride, raw)
	}
}

func TestAgentThreadAgentIDQueriesUseDirectBindingOnly(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"../../../sql/queries/agent_thread.sql",
		"../sqlc/agent_thread.sql.go",
		"../../../cmd/mcp-orch/store/agent/store.go",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(raw)
		for _, forbidden := range []string{
			"b.provider_thread_id = agent_threads.owner_thread_id",
			"b.codex_thread_id = agent_threads.owner_thread_id",
			"agent_threads.owner_thread_id <> ''",
			"b.provider_thread_id = t.owner_thread_id",
			"b.codex_thread_id = t.owner_thread_id",
			"t.owner_thread_id <> ''",
		} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s still infers agent_id from owner_thread_id via %q", path, forbidden)
			}
		}
	}
}

func TestAgentThreadConfigBatchSQLUsesTextArray(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want string
	}{
		{
			path: "../../../sql/queries/agent_thread.sql",
			want: "thread_id = ANY(sqlc.arg(thread_ids)::text[])",
		},
		{
			path: "../sqlc/agent_thread.sql.go",
			want: "thread_id = ANY($1::text[])",
		},
	}
	for _, tt := range tests {
		raw, err := os.ReadFile(tt.path)
		if err != nil {
			t.Fatalf("read %s: %v", tt.path, err)
		}
		text := string(raw)
		if !strings.Contains(text, tt.want) {
			t.Fatalf("%s missing array batch query %q", tt.path, tt.want)
		}
		if strings.Contains(text, "thread_id IN ($1)") || strings.Contains(text, "sqlc.slice('thread_ids')") {
			t.Fatalf("%s still encodes thread IDs as a single text parameter", tt.path)
		}
	}
}

func TestSaveAndLoadPromptSnapshot(t *testing.T) {
	t.Parallel()

	want := testPromptSnapshot()
	var saved sqlc.UpdateAgentThreadPromptSnapshotParams
	s := newPromptSnapshotStore(&saved)

	if err := s.SavePromptSnapshot(context.Background(), "thread-1", want); err != nil {
		t.Fatalf("SavePromptSnapshot() error = %v", err)
	}
	assertSavedPromptSnapshot(t, saved, want)

	got, err := s.LoadPromptSnapshot(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("LoadPromptSnapshot() error = %v", err)
	}
	if got == nil {
		t.Fatal("LoadPromptSnapshot() = nil, want snapshot")
	}
	assertStorePromptSnapshotEqual(t, "LoadPromptSnapshot()", *got, want)
}

func testPromptSnapshot() PromptSnapshot {
	return PromptSnapshot{
		DisplayName:           "thread-1",
		BaseInstructions:      "base",
		DeveloperInstructions: "dev",
		Provider:              "codex",
		Version:               1,
		Hash:                  "hash-1",
		SectionSnapshot:       map[string]string{"cwd": "/tmp/project", "date": "2026-04-14"},
		Generation:            9,
	}
}

func newPromptSnapshotStore(saved *sqlc.UpdateAgentThreadPromptSnapshotParams) *store {
	return &store{q: &threadQuerierStub{
		savePromptSnapshotFn: func(_ context.Context, arg sqlc.UpdateAgentThreadPromptSnapshotParams) (int64, error) {
			*saved = arg
			return 1, nil
		},
		loadPromptSnapshotFn: func(context.Context, string) ([]byte, error) {
			return []byte(`{"display_name":"thread-1","base_instructions":"base","developer_instructions":"dev","provider":"codex","version":1,"hash":"hash-1","section_snapshot":{"cwd":"/tmp/project","date":"2026-04-14"},"generation":9}`), nil
		},
	}}
}

func assertSavedPromptSnapshot(t *testing.T, saved sqlc.UpdateAgentThreadPromptSnapshotParams, want PromptSnapshot) {
	t.Helper()
	if saved.ThreadID != "thread-1" {
		t.Fatalf("SavePromptSnapshot() thread_id = %q, want %q", saved.ThreadID, "thread-1")
	}
	var stored PromptSnapshot
	if err := json.Unmarshal(saved.PromptSnapshot, &stored); err != nil {
		t.Fatalf("json.Unmarshal(saved prompt_snapshot) error = %v", err)
	}
	assertStorePromptSnapshotEqual(t, "stored prompt snapshot", stored, want)
}

func assertStorePromptSnapshotEqual(t *testing.T, label string, got, want PromptSnapshot) {
	t.Helper()
	assertStorePromptSnapshotMetadata(t, label, got, want)
	assertStorePromptSnapshotSections(t, label, got, want)
}

func assertStorePromptSnapshotMetadata(t *testing.T, label string, got, want PromptSnapshot) {
	t.Helper()
	if got.DisplayName != want.DisplayName {
		t.Fatalf("%s DisplayName = %q, want %q", label, got.DisplayName, want.DisplayName)
	}
	if got.BaseInstructions != want.BaseInstructions {
		t.Fatalf("%s BaseInstructions = %q, want %q", label, got.BaseInstructions, want.BaseInstructions)
	}
	if got.DeveloperInstructions != want.DeveloperInstructions {
		t.Fatalf("%s DeveloperInstructions = %q, want %q", label, got.DeveloperInstructions, want.DeveloperInstructions)
	}
	if got.Provider != want.Provider || got.Version != want.Version || got.Hash != want.Hash || got.Generation != want.Generation {
		t.Fatalf("%s metadata = %#v, want %#v", label, got, want)
	}
}

func assertStorePromptSnapshotSections(t *testing.T, label string, got, want PromptSnapshot) {
	t.Helper()
	if len(got.SectionSnapshot) != len(want.SectionSnapshot) {
		t.Fatalf("%s sections = %#v, want %#v", label, got.SectionSnapshot, want.SectionSnapshot)
	}
	if got.SectionSnapshot["cwd"] != want.SectionSnapshot["cwd"] {
		t.Fatalf("%s cwd section = %q, want %q", label, got.SectionSnapshot["cwd"], want.SectionSnapshot["cwd"])
	}
	if got.SectionSnapshot["date"] != want.SectionSnapshot["date"] {
		t.Fatalf("%s date section = %q, want %q", label, got.SectionSnapshot["date"], want.SectionSnapshot["date"])
	}
}

func TestSavePromptSnapshotMissingThread(t *testing.T) {
	t.Parallel()

	s := &store{q: &threadQuerierStub{
		savePromptSnapshotFn: func(context.Context, sqlc.UpdateAgentThreadPromptSnapshotParams) (int64, error) {
			return 0, nil
		},
	}}

	err := s.SavePromptSnapshot(context.Background(), "missing-thread", PromptSnapshot{})
	if !errors.Is(err, platformdb.ErrNotFound) {
		t.Fatalf("SavePromptSnapshot() error = %v, want ErrNotFound", err)
	}
}

func TestLoadPromptSnapshotNilPayload(t *testing.T) {
	t.Parallel()

	s := &store{q: &threadQuerierStub{
		loadPromptSnapshotFn: func(context.Context, string) ([]byte, error) {
			return nil, nil
		},
	}}

	got, err := s.LoadPromptSnapshot(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("LoadPromptSnapshot() error = %v", err)
	}
	if got != nil {
		t.Fatalf("LoadPromptSnapshot() = %#v, want nil", got)
	}
}
