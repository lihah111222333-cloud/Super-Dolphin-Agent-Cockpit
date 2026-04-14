package binding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type bindingQuerierStub struct {
	bindAgentThreadFn                  func(context.Context, sqlc.BindAgentThreadParams) error
	deleteAgentProviderBindingByIDFn   func(context.Context, string) error
	getAgentProviderBindingByAgentIDFn func(context.Context, string) (sqlc.GetAgentProviderBindingByAgentIDRow, error)
	getByProviderThreadFn              func(context.Context, sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.GetAgentProviderBindingByProviderThreadRow, error)
	getThreadByAgentFn                 func(context.Context, string) (string, error)
	listAgentThreadBindingsFn          func(context.Context) ([]sqlc.ListAgentThreadBindingsRow, error)
	unbindAgentThreadFn                func(context.Context, string) error
	updateAgentCwdFn                   func(context.Context, sqlc.UpdateAgentCwdParams) error
	updateArchivedFn                   func(context.Context, sqlc.UpdateAgentProviderBindingArchivedParams) error
	updateSessionUUIDFn                func(context.Context, sqlc.UpdateAgentProviderBindingSessionUUIDParams) error
	upsertAgentProviderBindingFn       func(context.Context, sqlc.UpsertAgentProviderBindingParams) error
}

func (s *bindingQuerierStub) BindAgentThread(ctx context.Context, arg sqlc.BindAgentThreadParams) error {
	if s.bindAgentThreadFn != nil {
		return s.bindAgentThreadFn(ctx, arg)
	}
	return nil
}

func (s *bindingQuerierStub) DeleteAgentProviderBindingByAgentID(ctx context.Context, agentID string) error {
	if s.deleteAgentProviderBindingByIDFn != nil {
		return s.deleteAgentProviderBindingByIDFn(ctx, agentID)
	}
	return nil
}

func (s *bindingQuerierStub) GetAgentProviderBindingByAgentID(ctx context.Context, agentID string) (sqlc.GetAgentProviderBindingByAgentIDRow, error) {
	if s.getAgentProviderBindingByAgentIDFn != nil {
		return s.getAgentProviderBindingByAgentIDFn(ctx, agentID)
	}
	return sqlc.GetAgentProviderBindingByAgentIDRow{}, nil
}

func (s *bindingQuerierStub) GetAgentProviderBindingByProviderThread(ctx context.Context, arg sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.GetAgentProviderBindingByProviderThreadRow, error) {
	if s.getByProviderThreadFn != nil {
		return s.getByProviderThreadFn(ctx, arg)
	}
	return sqlc.GetAgentProviderBindingByProviderThreadRow{}, nil
}

func (s *bindingQuerierStub) GetThreadByAgent(ctx context.Context, agentID string) (string, error) {
	if s.getThreadByAgentFn != nil {
		return s.getThreadByAgentFn(ctx, agentID)
	}
	return "", nil
}

func (s *bindingQuerierStub) ListAgentThreadBindings(ctx context.Context) ([]sqlc.ListAgentThreadBindingsRow, error) {
	if s.listAgentThreadBindingsFn != nil {
		return s.listAgentThreadBindingsFn(ctx)
	}
	return nil, nil
}

func (s *bindingQuerierStub) UnbindAgentThread(ctx context.Context, agentID string) error {
	if s.unbindAgentThreadFn != nil {
		return s.unbindAgentThreadFn(ctx, agentID)
	}
	return nil
}

func (s *bindingQuerierStub) UpdateAgentCwd(ctx context.Context, arg sqlc.UpdateAgentCwdParams) error {
	if s.updateAgentCwdFn != nil {
		return s.updateAgentCwdFn(ctx, arg)
	}
	return nil
}

func (s *bindingQuerierStub) UpdateAgentProviderBindingArchived(ctx context.Context, arg sqlc.UpdateAgentProviderBindingArchivedParams) error {
	if s.updateArchivedFn != nil {
		return s.updateArchivedFn(ctx, arg)
	}
	return nil
}

func (s *bindingQuerierStub) UpdateAgentProviderBindingSessionUUID(ctx context.Context, arg sqlc.UpdateAgentProviderBindingSessionUUIDParams) error {
	if s.updateSessionUUIDFn != nil {
		return s.updateSessionUUIDFn(ctx, arg)
	}
	return nil
}

func (s *bindingQuerierStub) UpsertAgentProviderBinding(ctx context.Context, arg sqlc.UpsertAgentProviderBindingParams) error {
	if s.upsertAgentProviderBindingFn != nil {
		return s.upsertAgentProviderBindingFn(ctx, arg)
	}
	return nil
}

func TestUpsertAgentProviderBinding(t *testing.T) {
	t.Parallel()

	params := UpsertParams{
		AgentID:          "agent-upsert",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-upsert",
		CodexThreadID:    "codex-thread-upsert",
		RolloutPath:      "/tmp/rollout-upsert",
		Cwd:              "/tmp/cwd-upsert",
		CreatedAt:        100,
		UpdatedAt:        200,
	}

	t.Run("success", func(t *testing.T) {
		var got sqlc.UpsertAgentProviderBindingParams
		lookupCalls := 0
		s := &store{q: &bindingQuerierStub{
			upsertAgentProviderBindingFn: func(_ context.Context, arg sqlc.UpsertAgentProviderBindingParams) error {
				got = arg
				return nil
			},
			getByProviderThreadFn: func(_ context.Context, arg sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.GetAgentProviderBindingByProviderThreadRow, error) {
				lookupCalls++
				return sqlc.GetAgentProviderBindingByProviderThreadRow{AgentID: arg.ProviderThreadID}, nil
			},
		}}

		if err := s.Upsert(context.Background(), params); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}
		if got.AgentID != params.AgentID || got.Provider != params.Provider || got.ProviderThreadID != params.ProviderThreadID {
			t.Fatalf("Upsert() forwarded wrong identity params: %+v", got)
		}
		if got.CodexThreadID != params.CodexThreadID || got.RolloutPath != params.RolloutPath || got.Cwd != params.Cwd {
			t.Fatalf("Upsert() forwarded wrong payload params: %+v", got)
		}
		if got.CreatedAt != params.CreatedAt || got.UpdatedAt != params.UpdatedAt {
			t.Fatalf("Upsert() forwarded wrong timestamps: %+v", got)
		}
		if lookupCalls != 0 {
			t.Fatalf("Upsert() lookupCalls = %d, want 0", lookupCalls)
		}
	})

	t.Run("unique violation fallback", func(t *testing.T) {
		lookupCalls := 0
		var gotLookup sqlc.GetAgentProviderBindingByProviderThreadParams
		s := &store{q: &bindingQuerierStub{
			upsertAgentProviderBindingFn: func(_ context.Context, arg sqlc.UpsertAgentProviderBindingParams) error {
				if arg.AgentID != params.AgentID {
					t.Fatalf("Upsert() AgentID = %q, want %q", arg.AgentID, params.AgentID)
				}
				return &pgconn.PgError{Code: "23505", Message: "duplicate key"}
			},
			getByProviderThreadFn: func(_ context.Context, arg sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.GetAgentProviderBindingByProviderThreadRow, error) {
				lookupCalls++
				gotLookup = arg
				return sqlc.GetAgentProviderBindingByProviderThreadRow{AgentID: params.AgentID}, nil
			},
		}}

		if err := s.Upsert(context.Background(), params); err != nil {
			t.Fatalf("Upsert() unique violation fallback error = %v", err)
		}
		if lookupCalls != 1 {
			t.Fatalf("Upsert() lookupCalls = %d, want 1", lookupCalls)
		}
		if gotLookup.Provider != params.Provider || gotLookup.ProviderThreadID != params.ProviderThreadID {
			t.Fatalf("Upsert() fallback lookup = %+v", gotLookup)
		}
	})
}

func TestGetByProviderThread(t *testing.T) {
	t.Parallel()

	t.Run("found", func(t *testing.T) {
		var got sqlc.GetAgentProviderBindingByProviderThreadParams
		s := &store{q: &bindingQuerierStub{
			getByProviderThreadFn: func(_ context.Context, arg sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.GetAgentProviderBindingByProviderThreadRow, error) {
				got = arg
				return sampleAgentProviderBindingByProviderThreadRow(), nil
			},
		}}

		binding, err := s.GetByProviderThread(context.Background(), "codex", "provider-thread-1")
		if err != nil {
			t.Fatalf("GetByProviderThread() error = %v", err)
		}
		if got.Provider != "codex" || got.ProviderThreadID != "provider-thread-1" {
			t.Fatalf("GetByProviderThread() lookup = %+v", got)
		}
		assertBinding(t, binding, sampleBinding())
	})

	t.Run("missing returns nil binding", func(t *testing.T) {
		s := &store{q: &bindingQuerierStub{
			getByProviderThreadFn: func(context.Context, sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.GetAgentProviderBindingByProviderThreadRow, error) {
				return sqlc.GetAgentProviderBindingByProviderThreadRow{}, pgx.ErrNoRows
			},
		}}

		binding, err := s.GetByProviderThread(context.Background(), "codex", "missing-thread")
		if binding != nil {
			t.Fatalf("GetByProviderThread() binding = %+v, want nil", binding)
		}
		assertStoreError(t, err, "get_by_provider_thread", "binding", pgx.ErrNoRows)
		if !errors.Is(err, platformdb.ErrNotFound) {
			t.Fatalf("GetByProviderThread() error = %v, want ErrNotFound", err)
		}
	})
}

func TestGetByAgentID(t *testing.T) {
	t.Parallel()

	t.Run("found", func(t *testing.T) {
		var got string
		s := &store{q: &bindingQuerierStub{
			getAgentProviderBindingByAgentIDFn: func(_ context.Context, agentID string) (sqlc.GetAgentProviderBindingByAgentIDRow, error) {
				got = agentID
				return sampleAgentProviderBindingByAgentIDRow(), nil
			},
		}}

		binding, err := s.GetByAgentID(context.Background(), "agent-lookup")
		if err != nil {
			t.Fatalf("GetByAgentID() error = %v", err)
		}
		if got != "agent-lookup" {
			t.Fatalf("GetByAgentID() agentID = %q, want %q", got, "agent-lookup")
		}
		assertBinding(t, binding, sampleBinding())
	})

	t.Run("missing returns nil binding", func(t *testing.T) {
		s := &store{q: &bindingQuerierStub{
			getAgentProviderBindingByAgentIDFn: func(context.Context, string) (sqlc.GetAgentProviderBindingByAgentIDRow, error) {
				return sqlc.GetAgentProviderBindingByAgentIDRow{}, pgx.ErrNoRows
			},
		}}

		binding, err := s.GetByAgentID(context.Background(), "missing-agent")
		if binding != nil {
			t.Fatalf("GetByAgentID() binding = %+v, want nil", binding)
		}
		assertStoreError(t, err, "get_by_agent_id", "binding", pgx.ErrNoRows)
		if !errors.Is(err, platformdb.ErrNotFound) {
			t.Fatalf("GetByAgentID() error = %v, want ErrNotFound", err)
		}
	})
}

func TestDeleteByAgentID(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		var got string
		s := &store{q: &bindingQuerierStub{
			deleteAgentProviderBindingByIDFn: func(_ context.Context, agentID string) error {
				got = agentID
				return nil
			},
		}}

		if err := s.DeleteByAgentID(context.Background(), "agent-delete"); err != nil {
			t.Fatalf("DeleteByAgentID() error = %v", err)
		}
		if got != "agent-delete" {
			t.Fatalf("DeleteByAgentID() agentID = %q, want %q", got, "agent-delete")
		}
	})

	t.Run("missing is noop", func(t *testing.T) {
		var calls int
		s := &store{q: &bindingQuerierStub{
			deleteAgentProviderBindingByIDFn: func(_ context.Context, agentID string) error {
				calls++
				if agentID != "missing-agent" {
					t.Fatalf("DeleteByAgentID() agentID = %q, want %q", agentID, "missing-agent")
				}
				return nil
			},
		}}

		if err := s.DeleteByAgentID(context.Background(), "missing-agent"); err != nil {
			t.Fatalf("DeleteByAgentID() missing error = %v", err)
		}
		if calls != 1 {
			t.Fatalf("DeleteByAgentID() calls = %d, want 1", calls)
		}
	})
}

func TestUpdateSessionUUID(t *testing.T) {
	t.Parallel()

	var got sqlc.UpdateAgentProviderBindingSessionUUIDParams
	s := &store{q: &bindingQuerierStub{
		updateSessionUUIDFn: func(_ context.Context, arg sqlc.UpdateAgentProviderBindingSessionUUIDParams) error {
			got = arg
			return nil
		},
	}}

	if err := s.UpdateSessionUUID(context.Background(), UpdateSessionUUIDParams{
		SessionUUID: "session-uuid-1",
		UpdatedAt:   300,
		AgentID:     "agent-session",
	}); err != nil {
		t.Fatalf("UpdateSessionUUID() error = %v", err)
	}
	if got.SessionUUID != "session-uuid-1" || got.UpdatedAt != 300 || got.AgentID != "agent-session" {
		t.Fatalf("UpdateSessionUUID() forwarded wrong params: %+v", got)
	}
}

func TestSetArchived(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		archived bool
	}{
		{name: "archive", archived: true},
		{name: "unarchive", archived: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got sqlc.UpdateAgentProviderBindingArchivedParams
			s := &store{q: &bindingQuerierStub{
				updateArchivedFn: func(_ context.Context, arg sqlc.UpdateAgentProviderBindingArchivedParams) error {
					got = arg
					return nil
				},
			}}

			if err := s.SetArchived(context.Background(), SetArchivedParams{
				AgentID:   "agent-archived",
				Archived:  tt.archived,
				UpdatedAt: 400,
			}); err != nil {
				t.Fatalf("SetArchived() error = %v", err)
			}
			if got.AgentID != "agent-archived" || got.Archived != tt.archived || got.UpdatedAt != 400 {
				t.Fatalf("SetArchived() forwarded wrong params: %+v", got)
			}
		})
	}
}

func TestErrorWrapping(t *testing.T) {
	t.Parallel()

	params := UpsertParams{
		AgentID:          "agent-wrap",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-wrap",
	}

	t.Run("generic delete error", func(t *testing.T) {
		baseErr := errors.New("delete failed")
		s := &store{q: &bindingQuerierStub{
			deleteAgentProviderBindingByIDFn: func(context.Context, string) error {
				return baseErr
			},
		}}

		err := s.DeleteByAgentID(context.Background(), "agent-wrap")
		assertStoreError(t, err, "delete_by_agent_id", "binding", baseErr)
	})

	t.Run("not found classification", func(t *testing.T) {
		s := &store{q: &bindingQuerierStub{
			getAgentProviderBindingByAgentIDFn: func(context.Context, string) (sqlc.GetAgentProviderBindingByAgentIDRow, error) {
				return sqlc.GetAgentProviderBindingByAgentIDRow{}, pgx.ErrNoRows
			},
		}}

		_, err := s.GetByAgentID(context.Background(), "missing-agent")
		assertStoreError(t, err, "get_by_agent_id", "binding", pgx.ErrNoRows)
		if !errors.Is(err, platformdb.ErrNotFound) {
			t.Fatalf("GetByAgentID() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("conflict classification", func(t *testing.T) {
		baseErr := &pgconn.PgError{Code: "23505", Message: "duplicate key"}
		s := &store{q: &bindingQuerierStub{
			upsertAgentProviderBindingFn: func(context.Context, sqlc.UpsertAgentProviderBindingParams) error {
				return baseErr
			},
			getByProviderThreadFn: func(context.Context, sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.GetAgentProviderBindingByProviderThreadRow, error) {
				return sqlc.GetAgentProviderBindingByProviderThreadRow{AgentID: "other-agent"}, nil
			},
		}}

		err := s.Upsert(context.Background(), params)
		assertStoreError(t, err, "upsert", "binding", baseErr)
		if !errors.Is(err, platformdb.ErrConflict) {
			t.Fatalf("Upsert() error = %v, want ErrConflict", err)
		}
	})
}

func TestBindAgentThread(t *testing.T) {
	t.Parallel()

	var got sqlc.BindAgentThreadParams
	s := &store{q: &bindingQuerierStub{
		bindAgentThreadFn: func(_ context.Context, arg sqlc.BindAgentThreadParams) error {
			got = arg
			return nil
		},
	}}

	start := time.Now().Unix()
	if err := s.BindAgentThread(context.Background(), BindAgentThreadParams{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
		Cwd:      "/tmp/run",
	}); err != nil {
		t.Fatalf("BindAgentThread() error = %v", err)
	}
	end := time.Now().Unix()

	if got.AgentID != "agent-1" || got.ThreadID != "thread-1" || got.Cwd != "/tmp/run" {
		t.Fatalf("BindAgentThread() forwarded wrong params: %+v", got)
	}
	if got.CreatedAt < start || got.CreatedAt > end {
		t.Fatalf("BindAgentThread() CreatedAt = %d, want between %d and %d", got.CreatedAt, start, end)
	}
	if got.UpdatedAt < start || got.UpdatedAt > end {
		t.Fatalf("BindAgentThread() UpdatedAt = %d, want between %d and %d", got.UpdatedAt, start, end)
	}
}

func TestBindAgentThreadSQLMatchesTriggerCompatibleSemantics(t *testing.T) {
	t.Parallel()

	sql := readBindAgentThreadSQL(t)
	columns := parseSQLList(extractSQLBlock(t, sql, "INSERT INTO agent_provider_binding (\n", "\n) VALUES ("))
	values := parseSQLList(extractSQLBlock(t, sql, ") VALUES (\n", "\n)\nON CONFLICT (agent_id) DO UPDATE"))
	setClause := strings.TrimSpace(extractSQLBlock(t, sql, "ON CONFLICT (agent_id) DO UPDATE\nSET ", ";"))
	providerIndex := indexOfSQLItem(t, columns, "provider_thread_id")
	codexIndex := indexOfSQLItem(t, columns, "codex_thread_id")
	if values[providerIndex] != "sqlc.arg(thread_id)" {
		t.Fatalf("provider_thread_id insert value = %q, want sqlc.arg(thread_id)", values[providerIndex])
	}
	if values[codexIndex] != "sqlc.arg(thread_id)" {
		t.Fatalf("codex_thread_id insert value = %q, want sqlc.arg(thread_id)", values[codexIndex])
	}
	if strings.Contains(setClause, "provider_thread_id =") {
		t.Fatalf("unexpected provider_thread_id update in ON CONFLICT SET: %q", setClause)
	}
	if !strings.Contains(setClause, "codex_thread_id = EXCLUDED.codex_thread_id") {
		t.Fatalf("missing codex_thread_id update in ON CONFLICT SET: %q", setClause)
	}
}

func TestUnbindAgentThread(t *testing.T) {
	t.Parallel()

	var got string
	s := &store{q: &bindingQuerierStub{
		unbindAgentThreadFn: func(_ context.Context, agentID string) error {
			got = agentID
			return nil
		},
	}}

	if err := s.UnbindAgentThread(context.Background(), "agent-2"); err != nil {
		t.Fatalf("UnbindAgentThread() error = %v", err)
	}
	if got != "agent-2" {
		t.Fatalf("UnbindAgentThread() agentID = %q, want %q", got, "agent-2")
	}
}

func TestGetThreadByAgent(t *testing.T) {
	t.Parallel()

	var got string
	s := &store{q: &bindingQuerierStub{
		getThreadByAgentFn: func(_ context.Context, agentID string) (string, error) {
			got = agentID
			return "thread-3", nil
		},
	}}

	threadID, err := s.GetThreadByAgent(context.Background(), "agent-3")
	if err != nil {
		t.Fatalf("GetThreadByAgent() error = %v", err)
	}
	if got != "agent-3" || threadID != "thread-3" {
		t.Fatalf("GetThreadByAgent() = (%q, %q), want (%q, %q)", got, threadID, "agent-3", "thread-3")
	}
}

func TestListAgentThreadBindings(t *testing.T) {
	t.Parallel()

	s := &store{q: &bindingQuerierStub{
		listAgentThreadBindingsFn: func(context.Context) ([]sqlc.ListAgentThreadBindingsRow, error) {
			return []sqlc.ListAgentThreadBindingsRow{{
				AgentID:          "agent-4",
				Provider:         "codex",
				ProviderThreadID: "provider-thread-4",
				CodexThreadID:    "codex-thread-4",
				RolloutPath:      "/tmp/rollout",
				Cwd:              "/tmp/cwd",
				Archived:         true,
				CreatedAt:        10,
				UpdatedAt:        20,
				SessionUUID:      "session-4",
			}}, nil
		},
	}}

	rows, err := s.ListAgentThreadBindings(context.Background())
	if err != nil {
		t.Fatalf("ListAgentThreadBindings() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListAgentThreadBindings() len = %d, want 1", len(rows))
	}
	if rows[0].ProviderThreadID != "provider-thread-4" || rows[0].CodexThreadID != "codex-thread-4" {
		t.Fatalf("ListAgentThreadBindings() = %+v", rows[0])
	}
}

func TestUpdateAgentCwd(t *testing.T) {
	t.Parallel()

	var got sqlc.UpdateAgentCwdParams
	s := &store{q: &bindingQuerierStub{
		updateAgentCwdFn: func(_ context.Context, arg sqlc.UpdateAgentCwdParams) error {
			got = arg
			return nil
		},
	}}

	start := time.Now().Unix()
	if err := s.UpdateAgentCwd(context.Background(), UpdateAgentCwdParams{
		AgentID: "agent-5",
		Cwd:     "/tmp/updated",
	}); err != nil {
		t.Fatalf("UpdateAgentCwd() error = %v", err)
	}
	end := time.Now().Unix()

	if got.AgentID != "agent-5" || got.Cwd != "/tmp/updated" {
		t.Fatalf("UpdateAgentCwd() forwarded wrong params: %+v", got)
	}
	if got.UpdatedAt < start || got.UpdatedAt > end {
		t.Fatalf("UpdateAgentCwd() UpdatedAt = %d, want between %d and %d", got.UpdatedAt, start, end)
	}
}

func readBindAgentThreadSQL(t *testing.T) string {
	t.Helper()

	path := filepath.Join("..", "..", "..", "sql", "queries", "thread_binding.sql")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	return string(content)
}

func extractSQLBlock(t *testing.T, sql, start, end string) string {
	t.Helper()

	startIndex := strings.Index(sql, start)
	if startIndex < 0 {
		t.Fatalf("start marker %q not found", start)
	}
	startIndex += len(start)
	endIndex := strings.Index(sql[startIndex:], end)
	if endIndex < 0 {
		t.Fatalf("end marker %q not found", end)
	}
	return sql[startIndex : startIndex+endIndex]
}

func parseSQLList(block string) []string {
	lines := strings.Split(block, "\n")
	items := make([]string, 0, len(lines))
	for _, line := range lines {
		item := strings.TrimSuffix(strings.TrimSpace(line), ",")
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func indexOfSQLItem(t *testing.T, items []string, want string) int {
	t.Helper()

	for i, item := range items {
		if item == want {
			return i
		}
	}
	t.Fatalf("item %q not found in %v", want, items)
	return -1
}

func sampleAgentProviderBindingByAgentIDRow() sqlc.GetAgentProviderBindingByAgentIDRow {
	return sqlc.GetAgentProviderBindingByAgentIDRow{
		AgentID:          "agent-sample",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-sample",
		CodexThreadID:    "codex-thread-sample",
		RolloutPath:      "/tmp/rollout-sample",
		Cwd:              "/tmp/cwd-sample",
		Archived:         true,
		CreatedAt:        11,
		UpdatedAt:        22,
		SessionUUID:      "session-sample",
	}
}

func sampleAgentProviderBindingByProviderThreadRow() sqlc.GetAgentProviderBindingByProviderThreadRow {
	return sqlc.GetAgentProviderBindingByProviderThreadRow{
		AgentID:          "agent-sample",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-sample",
		CodexThreadID:    "codex-thread-sample",
		RolloutPath:      "/tmp/rollout-sample",
		Cwd:              "/tmp/cwd-sample",
		Archived:         true,
		CreatedAt:        11,
		UpdatedAt:        22,
		SessionUUID:      "session-sample",
	}
}

func sampleBinding() Binding {
	return Binding{
		AgentID:          "agent-sample",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-sample",
		CodexThreadID:    "codex-thread-sample",
		RolloutPath:      "/tmp/rollout-sample",
		Cwd:              "/tmp/cwd-sample",
		Archived:         true,
		CreatedAt:        11,
		UpdatedAt:        22,
		SessionUUID:      "session-sample",
	}
}

func assertBinding(t *testing.T, got *Binding, want Binding) {
	t.Helper()

	if got == nil {
		t.Fatal("binding = nil, want non-nil")
	}
	if *got != want {
		t.Fatalf("binding = %+v, want %+v", *got, want)
	}
}

func assertStoreError(t *testing.T, err error, operation, entity string, baseErr error) {
	t.Helper()

	if err == nil {
		t.Fatal("error = nil, want non-nil")
	}
	var storeErr *platformdb.StoreError
	if !errors.As(err, &storeErr) {
		t.Fatalf("error = %T %v, want *platformdb.StoreError", err, err)
	}
	if storeErr.Operation != operation || storeErr.Entity != entity {
		t.Fatalf("StoreError = %+v, want operation=%q entity=%q", storeErr, operation, entity)
	}
	if !errors.Is(err, baseErr) {
		t.Fatalf("error = %v, want wrapped base error %v", err, baseErr)
	}
	if !strings.Contains(err.Error(), operation+" "+entity+":") {
		t.Fatalf("error message = %q, want operation/entity prefix", err.Error())
	}
}
