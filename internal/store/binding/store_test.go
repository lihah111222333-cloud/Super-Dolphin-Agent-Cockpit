package binding

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

func TestUpsertAgentProviderBinding(t *testing.T) {
	t.Parallel()

	params := sampleUpsertParams()

	t.Run("success", func(t *testing.T) {
		runUpsertSuccessCase(t, params)
	})

	t.Run("unique violation fallback", func(t *testing.T) {
		runUpsertUniqueFallbackCase(t, params)
	})
}

func sampleUpsertParams() UpsertParams {
	return UpsertParams{
		AgentID:          "agent-upsert",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-upsert",
		CodexThreadID:    "codex-thread-upsert",
		RolloutPath:      "/tmp/rollout-upsert",
		Cwd:              "/tmp/cwd-upsert",
		CreatedAt:        100,
		UpdatedAt:        200,
	}
}

func bindingUniqueViolationError(t *testing.T) error {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite unique fixture: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec("CREATE TABLE fixture (value TEXT UNIQUE)"); err != nil {
		t.Fatalf("create sqlite unique fixture: %v", err)
	}
	if _, err := database.Exec("INSERT INTO fixture (value) VALUES ('duplicate')"); err != nil {
		t.Fatalf("seed sqlite unique fixture: %v", err)
	}
	_, err = database.Exec("INSERT INTO fixture (value) VALUES ('duplicate')")
	if err == nil || !platformdb.IsUniqueViolation(err) {
		t.Fatalf("sqlite unique fixture error = %v", err)
	}
	return err
}

func runUpsertSuccessCase(t *testing.T, params UpsertParams) {
	t.Helper()

	var got sqlc.UpsertAgentProviderBindingParams
	lookupCalls := 0
	s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
		upsertAgentProviderBindingFn: func(_ context.Context, arg sqlc.UpsertAgentProviderBindingParams) error {
			got = arg
			return nil
		},
		getByProviderThreadFn: func(_ context.Context, arg sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.AgentProviderBinding, error) {
			lookupCalls++
			return sqlc.AgentProviderBinding{AgentID: arg.ProviderThreadID}, nil
		},
	})}

	if err := s.Upsert(context.Background(), params); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	assertUpsertForwardedParams(t, got, params)
	if lookupCalls != 0 {
		t.Fatalf("Upsert() lookupCalls = %d, want 0", lookupCalls)
	}
}

func runUpsertUniqueFallbackCase(t *testing.T, params UpsertParams) {
	t.Helper()

	lookupCalls := 0
	var gotLookup sqlc.GetAgentProviderBindingByProviderThreadParams
	s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
		upsertAgentProviderBindingFn: func(_ context.Context, arg sqlc.UpsertAgentProviderBindingParams) error {
			if arg.AgentID != params.AgentID {
				t.Fatalf("Upsert() AgentID = %q, want %q", arg.AgentID, params.AgentID)
			}
			return bindingUniqueViolationError(t)
		},
		getByProviderThreadFn: func(_ context.Context, arg sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.AgentProviderBinding, error) {
			lookupCalls++
			gotLookup = arg
			return sqlc.AgentProviderBinding{AgentID: params.AgentID}, nil
		},
	})}

	if err := s.Upsert(context.Background(), params); err != nil {
		t.Fatalf("Upsert() unique violation fallback error = %v", err)
	}
	if lookupCalls != 1 {
		t.Fatalf("Upsert() lookupCalls = %d, want 1", lookupCalls)
	}
	assertProviderThreadLookup(t, gotLookup, params)
}

func assertUpsertForwardedParams(t *testing.T, got sqlc.UpsertAgentProviderBindingParams, want UpsertParams) {
	t.Helper()

	if got.AgentID != want.AgentID || got.Provider != want.Provider || got.ProviderThreadID != want.ProviderThreadID {
		t.Fatalf("Upsert() forwarded wrong identity params: %+v", got)
	}
	if got.CodexThreadID != want.CodexThreadID || got.RolloutPath != want.RolloutPath || got.CWD != want.Cwd {
		t.Fatalf("Upsert() forwarded wrong payload params: %+v", got)
	}
	if got.CreatedAt != want.CreatedAt || got.UpdatedAt != want.UpdatedAt {
		t.Fatalf("Upsert() forwarded wrong timestamps: %+v", got)
	}
}

func assertProviderThreadLookup(
	t *testing.T,
	got sqlc.GetAgentProviderBindingByProviderThreadParams,
	want UpsertParams,
) {
	t.Helper()

	if got.Provider != want.Provider || got.ProviderThreadID != want.ProviderThreadID {
		t.Fatalf("Upsert() fallback lookup = %+v", got)
	}
}

func TestGetByProviderThread(t *testing.T) {
	t.Parallel()

	t.Run("found", func(t *testing.T) {
		var got sqlc.GetAgentProviderBindingByProviderThreadParams
		s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
			getByProviderThreadFn: func(_ context.Context, arg sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.AgentProviderBinding, error) {
				got = arg
				return sampleAgentProviderBindingByProviderThreadRow(), nil
			},
		})}

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
		s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
			getByProviderThreadFn: func(context.Context, sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.AgentProviderBinding, error) {
				return sqlc.AgentProviderBinding{}, sql.ErrNoRows
			},
		})}

		binding, err := s.GetByProviderThread(context.Background(), "codex", "missing-thread")
		if binding != nil {
			t.Fatalf("GetByProviderThread() binding = %+v, want nil", binding)
		}
		assertStoreError(t, err, "get_by_provider_thread", "binding", sql.ErrNoRows)
		if !errors.Is(err, platformdb.ErrNotFound) {
			t.Fatalf("GetByProviderThread() error = %v, want ErrNotFound", err)
		}
	})
}

func TestGetByAgentID(t *testing.T) {
	t.Parallel()

	t.Run("found", func(t *testing.T) {
		var got string
		s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
			getAgentProviderBindingByAgentIDFn: func(_ context.Context, agentID string) (sqlc.AgentProviderBinding, error) {
				got = agentID
				return sampleAgentProviderBindingByAgentIDRow(), nil
			},
		})}

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
		s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
			getAgentProviderBindingByAgentIDFn: func(context.Context, string) (sqlc.AgentProviderBinding, error) {
				return sqlc.AgentProviderBinding{}, sql.ErrNoRows
			},
		})}

		binding, err := s.GetByAgentID(context.Background(), "missing-agent")
		if binding != nil {
			t.Fatalf("GetByAgentID() binding = %+v, want nil", binding)
		}
		assertStoreError(t, err, "get_by_agent_id", "binding", sql.ErrNoRows)
		if !errors.Is(err, platformdb.ErrNotFound) {
			t.Fatalf("GetByAgentID() error = %v, want ErrNotFound", err)
		}
	})
}

func TestSessionBindingAdapterMapsArchived(t *testing.T) {
	t.Parallel()

	s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
		getAgentProviderBindingByAgentIDFn: func(_ context.Context, agentID string) (sqlc.AgentProviderBinding, error) {
			if agentID != "agent-lookup" {
				t.Fatalf("GetByAgentID() agentID = %q, want agent-lookup", agentID)
			}
			return sampleAgentProviderBindingByAgentIDRow(), nil
		},
	})}
	adapter := NewSessionBindingLookup(s)
	binding, err := adapter.GetByAgentID(context.Background(), "agent-lookup")
	if err != nil {
		t.Fatalf("GetByAgentID() error = %v", err)
	}
	if binding == nil || !binding.Archived {
		t.Fatalf("SessionBinding.Archived = %v, want true", binding)
	}
}

func TestDeleteByAgentID(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		var got string
		s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
			deleteAgentProviderBindingByIDFn: func(_ context.Context, agentID string) error {
				got = agentID
				return nil
			},
		})}

		if err := s.DeleteByAgentID(context.Background(), "agent-delete"); err != nil {
			t.Fatalf("DeleteByAgentID() error = %v", err)
		}
		if got != "agent-delete" {
			t.Fatalf("DeleteByAgentID() agentID = %q, want %q", got, "agent-delete")
		}
	})

	t.Run("missing is noop", func(t *testing.T) {
		var calls int
		s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
			deleteAgentProviderBindingByIDFn: func(_ context.Context, agentID string) error {
				calls++
				if agentID != "missing-agent" {
					t.Fatalf("DeleteByAgentID() agentID = %q, want %q", agentID, "missing-agent")
				}
				return nil
			},
		})}

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
	s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
		updateSessionUUIDFn: func(_ context.Context, arg sqlc.UpdateAgentProviderBindingSessionUUIDParams) error {
			got = arg
			return nil
		},
	})}

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

func TestUpdateProviderThreadID(t *testing.T) {
	t.Parallel()

	var got sqlc.UpdateAgentProviderBindingProviderThreadIDParams
	s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
		updateProviderThreadIDFn: func(_ context.Context, arg sqlc.UpdateAgentProviderBindingProviderThreadIDParams) error {
			got = arg
			return nil
		},
	})}

	if err := s.UpdateProviderThreadID(context.Background(), UpdateProviderThreadIDParams{
		ProviderThreadID: "provider-thread-1",
		UpdatedAt:        350,
		AgentID:          "agent-provider-thread",
	}); err != nil {
		t.Fatalf("UpdateProviderThreadID() error = %v", err)
	}
	if got.ProviderThreadID != "provider-thread-1" || got.UpdatedAt != 350 || got.AgentID != "agent-provider-thread" {
		t.Fatalf("UpdateProviderThreadID() forwarded wrong params: %+v", got)
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
			s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
				updateArchivedFn: func(_ context.Context, arg sqlc.UpdateAgentProviderBindingArchivedParams) error {
					got = arg
					return nil
				},
			})}

			if err := s.SetArchived(context.Background(), SetArchivedParams{
				AgentID:   "agent-archived",
				Archived:  tt.archived,
				UpdatedAt: 400,
			}); err != nil {
				t.Fatalf("SetArchived() error = %v", err)
			}
			wantArchived := int64(0)
			if tt.archived {
				wantArchived = 1
			}
			if got.AgentID != "agent-archived" || got.Archived != wantArchived || got.UpdatedAt != 400 {
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
		s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
			deleteAgentProviderBindingByIDFn: func(context.Context, string) error {
				return baseErr
			},
		})}

		err := s.DeleteByAgentID(context.Background(), "agent-wrap")
		assertStoreError(t, err, "delete_by_agent_id", "binding", baseErr)
	})

	t.Run("not found classification", func(t *testing.T) {
		s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
			getAgentProviderBindingByAgentIDFn: func(context.Context, string) (sqlc.AgentProviderBinding, error) {
				return sqlc.AgentProviderBinding{}, sql.ErrNoRows
			},
		})}

		_, err := s.GetByAgentID(context.Background(), "missing-agent")
		assertStoreError(t, err, "get_by_agent_id", "binding", sql.ErrNoRows)
		if !errors.Is(err, platformdb.ErrNotFound) {
			t.Fatalf("GetByAgentID() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("conflict classification", func(t *testing.T) {
		baseErr := bindingUniqueViolationError(t)
		s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
			upsertAgentProviderBindingFn: func(context.Context, sqlc.UpsertAgentProviderBindingParams) error {
				return baseErr
			},
			getByProviderThreadFn: func(context.Context, sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.AgentProviderBinding, error) {
				return sqlc.AgentProviderBinding{AgentID: "other-agent"}, nil
			},
		})}

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
	s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
		bindAgentThreadFn: func(_ context.Context, arg sqlc.BindAgentThreadParams) error {
			got = arg
			return nil
		},
	})}

	start := time.Now().Unix()
	if err := s.BindAgentThread(context.Background(), BindAgentThreadParams{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
		Cwd:      "/tmp/run",
	}); err != nil {
		t.Fatalf("BindAgentThread() error = %v", err)
	}
	end := time.Now().Unix()

	if got.AgentID != "agent-1" || got.ThreadID != "thread-1" || got.CWD != "/tmp/run" {
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
	s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
		unbindAgentThreadFn: func(_ context.Context, agentID string) error {
			got = agentID
			return nil
		},
	})}

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
	s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
		getThreadByAgentFn: func(_ context.Context, agentID string) (string, error) {
			got = agentID
			return "thread-3", nil
		},
	})}

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

	s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
		listAgentThreadBindingsFn: func(context.Context) ([]sqlc.AgentProviderBinding, error) {
			return []sqlc.AgentProviderBinding{{
				AgentID:          "agent-4",
				Provider:         "codex",
				ProviderThreadID: "provider-thread-4",
				CodexThreadID:    "codex-thread-4",
				RolloutPath:      "/tmp/rollout",
				CWD:              "/tmp/cwd",
				Archived:         1,
				CreatedAt:        10,
				UpdatedAt:        20,
				SessionUUID:      "session-4",
			}}, nil
		},
	})}

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
	s := &store{q: newBindingQuerierTestAdapter(&bindingQuerierStub{
		updateAgentCwdFn: func(_ context.Context, arg sqlc.UpdateAgentCwdParams) error {
			got = arg
			return nil
		},
	})}

	start := time.Now().Unix()
	if err := s.UpdateAgentCwd(context.Background(), UpdateAgentCwdParams{
		AgentID: "agent-5",
		Cwd:     "/tmp/updated",
	}); err != nil {
		t.Fatalf("UpdateAgentCwd() error = %v", err)
	}
	end := time.Now().Unix()

	if got.AgentID != "agent-5" || got.CWD != "/tmp/updated" {
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
	return strings.ReplaceAll(string(content), "\r\n", "\n")
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

func sampleAgentProviderBindingByAgentIDRow() sqlc.AgentProviderBinding {
	return sqlc.AgentProviderBinding{
		AgentID:          "agent-sample",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-sample",
		CodexThreadID:    "codex-thread-sample",
		RolloutPath:      "/tmp/rollout-sample",
		CWD:              "/tmp/cwd-sample",
		Archived:         1,
		CreatedAt:        11,
		UpdatedAt:        22,
		SessionUUID:      "session-sample",
	}
}

func sampleAgentProviderBindingByProviderThreadRow() sqlc.AgentProviderBinding {
	return sqlc.AgentProviderBinding{
		AgentID:          "agent-sample",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-sample",
		CodexThreadID:    "codex-thread-sample",
		RolloutPath:      "/tmp/rollout-sample",
		CWD:              "/tmp/cwd-sample",
		Archived:         1,
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
