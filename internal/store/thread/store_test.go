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

func (s *threadQuerierStub) AgentThreadRunningExists(context.Context, sqlc.AgentThreadRunningExistsParams) (bool, error) {
	return false, nil
}

func (s *threadQuerierStub) AgentThreadExists(context.Context, sqlc.AgentThreadExistsParams) (bool, error) {
	return false, nil
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

func (s *threadQuerierStub) LoadAgentThreadPromptSnapshot(ctx context.Context, arg sqlc.LoadAgentThreadPromptSnapshotParams) ([]byte, error) {
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
	s := &store{q: &threadQuerierStub{
		getByIDFn: func(context.Context, string) (sqlc.GetAgentThreadByIDRow, error) {
			return sqlc.GetAgentThreadByIDRow{
				ThreadID:         "thread-1",
				Name:             "display name",
				ParentAgentID:    "agent-root",
				AgentType:        "reviewer",
				AgentMemoryScope: "project",
				ConfigOverride:   raw,
			}, nil
		},
		listAllFn: func(context.Context) ([]sqlc.ListAgentThreadsRow, error) {
			return []sqlc.ListAgentThreadsRow{{ThreadID: "thread-1", Name: "display name", ParentAgentID: "agent-root", AgentType: "reviewer", AgentMemoryScope: "project", ConfigOverride: raw}}, nil
		},
		listRunningFn: func(context.Context) ([]sqlc.ListRunningAgentThreadsRow, error) {
			return []sqlc.ListRunningAgentThreadsRow{{ThreadID: "thread-1", Name: "display name", ParentAgentID: "agent-root", AgentType: "reviewer", AgentMemoryScope: "project", ConfigOverride: raw}}, nil
		},
		listRecoverableFn: func(context.Context) ([]sqlc.ListRecoverableAgentThreadsRow, error) {
			return []sqlc.ListRecoverableAgentThreadsRow{{ThreadID: "thread-1", Name: "display name", ParentAgentID: "agent-root", AgentType: "reviewer", AgentMemoryScope: "project", ConfigOverride: raw}}, nil
		},
	}}

	thread, err := s.GetByThreadID(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("GetByThreadID() error = %v", err)
	}
	if thread.ParentAgentID != "agent-root" || thread.AgentType != "reviewer" || thread.AgentMemoryScope != "project" {
		t.Fatalf("GetByThreadID() identity = %#v, want parent/type/scope mapped", thread)
	}
	if thread.Name != "display name" {
		t.Fatalf("GetByThreadID().Name = %q, want display name", thread.Name)
	}
	if string(thread.ConfigOverride) != string(raw) {
		t.Fatalf("GetByThreadID().ConfigOverride = %s, want %s", thread.ConfigOverride, raw)
	}

	assertListConfigOverride := func(label string, threads []Thread) {
		t.Helper()
		if len(threads) != 1 || string(threads[0].ConfigOverride) != string(raw) {
			t.Fatalf("%s ConfigOverride = %#v, want %s", label, threads, raw)
		}
		if threads[0].ParentAgentID != "agent-root" || threads[0].AgentType != "reviewer" || threads[0].AgentMemoryScope != "project" {
			t.Fatalf("%s identity = %#v, want parent/type/scope mapped", label, threads[0])
		}
		if threads[0].Name != "display name" {
			t.Fatalf("%s Name = %q, want display name", label, threads[0].Name)
		}
	}

	all, err := s.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll() error = %v", err)
	}
	assertListConfigOverride("ListAll()", all)

	running, err := s.ListRunning(context.Background())
	if err != nil {
		t.Fatalf("ListRunning() error = %v", err)
	}
	assertListConfigOverride("ListRunning()", running)

	recoverable, err := s.ListRecoverable(context.Background())
	if err != nil {
		t.Fatalf("ListRecoverable() error = %v", err)
	}
	assertListConfigOverride("ListRecoverable()", recoverable)
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

func TestSaveAndLoadPromptSnapshot(t *testing.T) {
	t.Parallel()

	want := PromptSnapshot{
		DisplayName:           "thread-1",
		BaseInstructions:      "base",
		DeveloperInstructions: "dev",
		Provider:              "codex",
		Version:               1,
		Hash:                  "hash-1",
		SectionSnapshot: map[string]string{
			"cwd":  "/tmp/project",
			"date": "2026-04-14",
		},
		Generation: 9,
	}
	var saved sqlc.UpdateAgentThreadPromptSnapshotParams
	s := &store{q: &threadQuerierStub{
		savePromptSnapshotFn: func(_ context.Context, arg sqlc.UpdateAgentThreadPromptSnapshotParams) (int64, error) {
			saved = arg
			return 1, nil
		},
		loadPromptSnapshotFn: func(context.Context, string) ([]byte, error) {
			return []byte(`{"display_name":"thread-1","base_instructions":"base","developer_instructions":"dev","provider":"codex","version":1,"hash":"hash-1","section_snapshot":{"cwd":"/tmp/project","date":"2026-04-14"},"generation":9}`), nil
		},
	}}

	if err := s.SavePromptSnapshot(context.Background(), "thread-1", want); err != nil {
		t.Fatalf("SavePromptSnapshot() error = %v", err)
	}
	if saved.ThreadID != "thread-1" {
		t.Fatalf("SavePromptSnapshot() thread_id = %q, want %q", saved.ThreadID, "thread-1")
	}
	var stored PromptSnapshot
	if err := json.Unmarshal(saved.PromptSnapshot, &stored); err != nil {
		t.Fatalf("json.Unmarshal(saved prompt_snapshot) error = %v", err)
	}
	if stored.DisplayName != want.DisplayName ||
		stored.BaseInstructions != want.BaseInstructions ||
		stored.DeveloperInstructions != want.DeveloperInstructions ||
		stored.Provider != want.Provider ||
		stored.Version != want.Version ||
		stored.Hash != want.Hash ||
		stored.Generation != want.Generation ||
		len(stored.SectionSnapshot) != len(want.SectionSnapshot) ||
		stored.SectionSnapshot["cwd"] != want.SectionSnapshot["cwd"] ||
		stored.SectionSnapshot["date"] != want.SectionSnapshot["date"] {
		t.Fatalf("stored prompt snapshot = %#v, want %#v", stored, want)
	}

	got, err := s.LoadPromptSnapshot(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("LoadPromptSnapshot() error = %v", err)
	}
	if got == nil {
		t.Fatal("LoadPromptSnapshot() = nil, want snapshot")
	}
	if got.DisplayName != want.DisplayName ||
		got.BaseInstructions != want.BaseInstructions ||
		got.DeveloperInstructions != want.DeveloperInstructions ||
		got.Provider != want.Provider ||
		got.Version != want.Version ||
		got.Hash != want.Hash ||
		got.Generation != want.Generation ||
		got.SectionSnapshot["cwd"] != want.SectionSnapshot["cwd"] ||
		got.SectionSnapshot["date"] != want.SectionSnapshot["date"] {
		t.Fatalf("LoadPromptSnapshot() = %#v, want %#v", got, want)
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
