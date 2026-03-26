package thread

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type threadQuerierStub struct {
	getByIDFn         func(context.Context, string) (sqlc.GetAgentThreadByIDRow, error)
	listAllFn         func(context.Context) ([]sqlc.ListAgentThreadsRow, error)
	listRecoverableFn func(context.Context) ([]sqlc.ListRecoverableAgentThreadsRow, error)
	listRunningFn     func(context.Context) ([]sqlc.ListRunningAgentThreadsRow, error)
	upsertFn          func(context.Context, sqlc.UpsertAgentThreadParams) error
}

func (s *threadQuerierStub) AgentThreadRunningExists(context.Context, string) (bool, error) {
	return false, nil
}

func (s *threadQuerierStub) DeleteAgentThreadByID(context.Context, string) error { return nil }

func (s *threadQuerierStub) ExpireStaleAgentThreads(context.Context, sqlc.ExpireStaleAgentThreadsParams) (int64, error) {
	return 0, nil
}

func (s *threadQuerierStub) GetAgentThreadByID(ctx context.Context, threadID string) (sqlc.GetAgentThreadByIDRow, error) {
	if s.getByIDFn != nil {
		return s.getByIDFn(ctx, threadID)
	}
	return sqlc.GetAgentThreadByIDRow{}, nil
}

func (*threadQuerierStub) GetAgentThreadByPort(context.Context, int32) (sqlc.GetAgentThreadByPortRow, error) {
	return sqlc.GetAgentThreadByPortRow{}, nil
}

func (*threadQuerierStub) ListAgentThreadCwds(context.Context) ([]sqlc.ListAgentThreadCwdsRow, error) {
	return nil, nil
}

func (*threadQuerierStub) ListAgentThreadCwdsByPrefix(context.Context, *string) ([]sqlc.ListAgentThreadCwdsByPrefixRow, error) {
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

func (*threadQuerierStub) ResetRunningAgentThreads(context.Context) error { return nil }

func (*threadQuerierStub) UpdateAgentThreadStatus(context.Context, sqlc.UpdateAgentThreadStatusParams) error {
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
		ThreadID:       "thread-1",
		Model:          "gpt-5.4",
		ConfigOverride: raw,
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
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
				ThreadID:       "thread-1",
				ConfigOverride: raw,
			}, nil
		},
		listAllFn: func(context.Context) ([]sqlc.ListAgentThreadsRow, error) {
			return []sqlc.ListAgentThreadsRow{{ThreadID: "thread-1", ConfigOverride: raw}}, nil
		},
		listRunningFn: func(context.Context) ([]sqlc.ListRunningAgentThreadsRow, error) {
			return []sqlc.ListRunningAgentThreadsRow{{ThreadID: "thread-1", ConfigOverride: raw}}, nil
		},
		listRecoverableFn: func(context.Context) ([]sqlc.ListRecoverableAgentThreadsRow, error) {
			return []sqlc.ListRecoverableAgentThreadsRow{{ThreadID: "thread-1", ConfigOverride: raw}}, nil
		},
	}}

	thread, err := s.GetByThreadID(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("GetByThreadID() error = %v", err)
	}
	if string(thread.ConfigOverride) != string(raw) {
		t.Fatalf("GetByThreadID().ConfigOverride = %s, want %s", thread.ConfigOverride, raw)
	}

	assertListConfigOverride := func(label string, threads []Thread) {
		t.Helper()
		if len(threads) != 1 || string(threads[0].ConfigOverride) != string(raw) {
			t.Fatalf("%s ConfigOverride = %#v, want %s", label, threads, raw)
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
