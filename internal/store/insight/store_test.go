package insight

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

// insightQuerierStub is a recording double for the six queries the store
// consumes. Each *Fn defaults to a safe no-op return when not wired by a
// specific test.
type insightQuerierStub struct {
	upsertFn         func(context.Context, sqlc.UpsertSessionInsightParams) (sqlc.UpsertSessionInsightRow, error)
	getByLocalFn     func(context.Context, sqlc.GetSessionInsightByLocalTurnParams) (sqlc.GetSessionInsightByLocalTurnRow, error)
	listByThreadFn   func(context.Context, sqlc.ListSessionInsightsByThreadParams) ([]sqlc.ListSessionInsightsByThreadRow, error)
	listRecentFn     func(context.Context, sqlc.ListRecentSessionInsightsParams) ([]sqlc.ListRecentSessionInsightsRow, error)
	listApprovalFn   func(context.Context, sqlc.ListObservedApprovalRequestsParams) ([]sqlc.ListObservedApprovalRequestsRow, error)
	listTokenTurnsFn func(context.Context, sqlc.ListObservedTokenTurnsParams) ([]sqlc.ListObservedTokenTurnsRow, error)
}

func (s *insightQuerierStub) UpsertSessionInsight(ctx context.Context, a sqlc.UpsertSessionInsightParams) (sqlc.UpsertSessionInsightRow, error) {
	if s.upsertFn != nil {
		return s.upsertFn(ctx, a)
	}
	return sqlc.UpsertSessionInsightRow{ID: 1, ThreadID: a.ThreadID, LocalTurnID: a.LocalTurnID, Status: a.Status}, nil
}
func (s *insightQuerierStub) GetSessionInsightByLocalTurn(ctx context.Context, a sqlc.GetSessionInsightByLocalTurnParams) (sqlc.GetSessionInsightByLocalTurnRow, error) {
	if s.getByLocalFn != nil {
		return s.getByLocalFn(ctx, a)
	}
	return sqlc.GetSessionInsightByLocalTurnRow{ThreadID: a.ThreadID, LocalTurnID: a.LocalTurnID}, nil
}
func (s *insightQuerierStub) ListSessionInsightsByThread(ctx context.Context, a sqlc.ListSessionInsightsByThreadParams) ([]sqlc.ListSessionInsightsByThreadRow, error) {
	if s.listByThreadFn != nil {
		return s.listByThreadFn(ctx, a)
	}
	return nil, nil
}
func (s *insightQuerierStub) ListRecentSessionInsights(ctx context.Context, arg sqlc.ListRecentSessionInsightsParams) ([]sqlc.ListRecentSessionInsightsRow, error) {
	if s.listRecentFn != nil {
		return s.listRecentFn(ctx, arg)
	}
	return nil, nil
}
func (s *insightQuerierStub) ListObservedApprovalRequests(ctx context.Context, a sqlc.ListObservedApprovalRequestsParams) ([]sqlc.ListObservedApprovalRequestsRow, error) {
	if s.listApprovalFn != nil {
		return s.listApprovalFn(ctx, a)
	}
	return nil, nil
}
func (s *insightQuerierStub) ListObservedTokenTurns(ctx context.Context, a sqlc.ListObservedTokenTurnsParams) ([]sqlc.ListObservedTokenTurnsRow, error) {
	if s.listTokenTurnsFn != nil {
		return s.listTokenTurnsFn(ctx, a)
	}
	return nil, nil
}

// ----- Upsert contract tests -----

func TestUpsertDefaultsStatusAndSkills(t *testing.T) {
	t.Parallel()

	var got sqlc.UpsertSessionInsightParams
	s := &store{q: &insightQuerierStub{
		upsertFn: func(_ context.Context, a sqlc.UpsertSessionInsightParams) (sqlc.UpsertSessionInsightRow, error) {
			got = a
			return sqlc.UpsertSessionInsightRow{ID: 1, Status: a.Status}, nil
		},
	}}
	_, err := s.Upsert(context.Background(), UpsertParams{
		ThreadID: "t", LocalTurnID: "lt",
	})
	if err != nil {
		t.Fatalf("Upsert error = %v", err)
	}
	if got.Status != StatusUnknown {
		t.Fatalf("Status default = %q, want %q", got.Status, StatusUnknown)
	}
	if string(got.SkillsSelected) != "[]" {
		t.Fatalf("SkillsSelected default = %q, want []", got.SkillsSelected)
	}
	if got.CreatedAt == 0 || got.UpdatedAt == 0 {
		t.Fatalf("timestamps must default to now: created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
	}
}

func TestUpsertForwardsSuccessPointer(t *testing.T) {
	t.Parallel()
	var got sqlc.UpsertSessionInsightParams
	s := &store{q: &insightQuerierStub{
		upsertFn: func(_ context.Context, a sqlc.UpsertSessionInsightParams) (sqlc.UpsertSessionInsightRow, error) {
			got = a
			return sqlc.UpsertSessionInsightRow{ID: 1, Success: a.Success}, nil
		},
	}}
	fval := false
	_, err := s.Upsert(context.Background(), UpsertParams{
		ThreadID: "t", LocalTurnID: "lt", Success: &fval,
	})
	if err != nil {
		t.Fatalf("Upsert error = %v", err)
	}
	if got.Success == nil || *got.Success != 0 {
		t.Fatalf("Success = %v, want pointer to false", got.Success)
	}
	// A nil Success must reach sqlc as nil — this keeps the "unknown"
	// signal alive through the write path and prevents the default-bool
	// trap called out by the P3 plan.
	_, err = s.Upsert(context.Background(), UpsertParams{ThreadID: "t", LocalTurnID: "lt"})
	if err != nil {
		t.Fatalf("Upsert error = %v", err)
	}
	if got.Success != nil {
		t.Fatalf("Success must stay nil when not set, got %v", got.Success)
	}
}

// ----- GetByLocalTurn -----

func TestGetByLocalTurnRejectsEmptyIDs(t *testing.T) {
	t.Parallel()
	s := &store{q: &insightQuerierStub{}}
	_, err := s.GetByLocalTurn(context.Background(), "", "lt")
	if !errors.Is(err, ErrEmptyID) {
		t.Fatalf("empty thread_id: want ErrEmptyID, got %v", err)
	}
	_, err = s.GetByLocalTurn(context.Background(), "t", "")
	if !errors.Is(err, ErrEmptyID) {
		t.Fatalf("empty local_turn_id: want ErrEmptyID, got %v", err)
	}
}

func TestGetByLocalTurnMapsNotFound(t *testing.T) {
	t.Parallel()
	s := &store{q: &insightQuerierStub{
		getByLocalFn: func(context.Context, sqlc.GetSessionInsightByLocalTurnParams) (sqlc.GetSessionInsightByLocalTurnRow, error) {
			return sqlc.GetSessionInsightByLocalTurnRow{}, platformdb.ErrNotFound
		},
	}}
	_, err := s.GetByLocalTurn(context.Background(), "t", "lt")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// ----- List* -----

func TestListByThreadDefaultsLimit(t *testing.T) {
	t.Parallel()
	var got sqlc.ListSessionInsightsByThreadParams
	s := &store{q: &insightQuerierStub{
		listByThreadFn: func(_ context.Context, a sqlc.ListSessionInsightsByThreadParams) ([]sqlc.ListSessionInsightsByThreadRow, error) {
			got = a
			return nil, nil
		},
	}}
	_, err := s.ListByThread(context.Background(), "t", 0)
	if err != nil {
		t.Fatalf("ListByThread error = %v", err)
	}
	if got.Limit != 100 {
		t.Fatalf("Limit default = %d, want 100", got.Limit)
	}
}

func TestListRecentPassesThrough(t *testing.T) {
	t.Parallel()
	var gotLimit int64
	s := &store{q: &insightQuerierStub{
		listRecentFn: func(_ context.Context, arg sqlc.ListRecentSessionInsightsParams) ([]sqlc.ListRecentSessionInsightsRow, error) {
			limit := arg.Limit
			gotLimit = limit
			return []sqlc.ListRecentSessionInsightsRow{{ID: 42, ThreadID: "t", LocalTurnID: "lt"}}, nil
		},
	}}
	rows, err := s.ListRecent(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListRecent error = %v", err)
	}
	if gotLimit != 10 || len(rows) != 1 || rows[0].ID != 42 {
		t.Fatalf("ListRecent = %+v (limit=%d)", rows, gotLimit)
	}
}

func TestListMethodsRejectOversizedLimit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		run  func(*store) error
	}{
		{
			name: "recent",
			run: func(s *store) error {
				_, err := s.ListRecent(context.Background(), 501)
				return err
			},
		},
		{
			name: "by thread",
			run: func(s *store) error {
				_, err := s.ListByThread(context.Background(), "thread-1", 501)
				return err
			},
		},
		{
			name: "approval requests",
			run: func(s *store) error {
				_, err := s.ListObservedApprovalRequests(context.Background(), "thread-1", 501)
				return err
			},
		},
		{
			name: "token turns",
			run: func(s *store) error {
				_, err := s.ListObservedTokenTurns(context.Background(), "thread-1", 501)
				return err
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			called := false
			s := &store{q: &insightQuerierStub{
				listByThreadFn: func(context.Context, sqlc.ListSessionInsightsByThreadParams) ([]sqlc.ListSessionInsightsByThreadRow, error) {
					called = true
					return nil, nil
				},
				listRecentFn: func(context.Context, sqlc.ListRecentSessionInsightsParams) ([]sqlc.ListRecentSessionInsightsRow, error) {
					called = true
					return nil, nil
				},
				listApprovalFn: func(context.Context, sqlc.ListObservedApprovalRequestsParams) ([]sqlc.ListObservedApprovalRequestsRow, error) {
					called = true
					return nil, nil
				},
				listTokenTurnsFn: func(context.Context, sqlc.ListObservedTokenTurnsParams) ([]sqlc.ListObservedTokenTurnsRow, error) {
					called = true
					return nil, nil
				},
			}}
			err := tc.run(s)
			if err == nil || !strings.Contains(err.Error(), "limit") {
				t.Fatalf("oversized limit error = %v, want limit rejection", err)
			}
			if called {
				t.Fatal("oversized limit reached sqlc query")
			}
		})
	}
}

// ----- observed-filter query lint -----

func TestListObservedApprovalRequestsForwardsThreadID(t *testing.T) {
	t.Parallel()
	var got sqlc.ListObservedApprovalRequestsParams
	s := &store{q: &insightQuerierStub{
		listApprovalFn: func(_ context.Context, a sqlc.ListObservedApprovalRequestsParams) ([]sqlc.ListObservedApprovalRequestsRow, error) {
			got = a
			return []sqlc.ListObservedApprovalRequestsRow{{ID: 1, ThreadID: "t", ApprovalRequests: 3}}, nil
		},
	}}
	rows, err := s.ListObservedApprovalRequests(context.Background(), "  t  ", 0)
	if err != nil {
		t.Fatalf("ListObservedApprovalRequests error = %v", err)
	}
	if got.Column1 != "t" {
		t.Fatalf("thread_id filter = %q, want trimmed t", got.Column1)
	}
	if got.Limit != 100 {
		t.Fatalf("Limit default = %d, want 100", got.Limit)
	}
	if len(rows) != 1 || rows[0].ApprovalRequests != 3 {
		t.Fatalf("rows = %+v", rows)
	}
}

// ----- migration + query lint (schema-level guarantees) -----

func TestMigration0046HasPartialUniqueIndexes(t *testing.T) {
	t.Parallel()
	sql := readRepoFile(t, filepath.Join("migrations", "0046_session_insights.sql"))
	for _, want := range []string{
		"uq_session_insights_local_turn",
		"uq_session_insights_provider_turn",
		"WHERE thread_id <> '' AND local_turn_id <> ''",
		"WHERE provider <> '' AND agent_id <> '' AND provider_turn_id <> ''",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration 0046 missing %q", want)
		}
	}
}

func TestUpsertQueryEnforcesPrecedenceAndNoRegression(t *testing.T) {
	t.Parallel()
	sql := readRepoFile(t, filepath.Join("sql", "queries", "session_insight.sql"))

	// status / success precedence must guard against interrupted/aborted
	// being displaced by a later 'completed'.
	for _, want := range []string{
		"WHEN session_insights.status IN ('interrupted', 'aborted')",
		"ELSE EXCLUDED.status",
		"ELSE EXCLUDED.success",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("upsert query missing precedence fragment %q", want)
		}
	}

	// token 计数类字段只允许单调前进，避免旧 insight 覆盖较新的用量统计。
	for _, col := range []string{"token_input", "token_output", "token_total",
		"context_window_tokens", "tool_calls", "tool_failures", "approval_requests",
		"duration_ms"} {
		want := "CASE WHEN (session_insights." + col + ") > (EXCLUDED." + col + ") THEN (session_insights." + col + ") ELSE (EXCLUDED." + col + ") END"
		if !strings.Contains(sql, want) {
			t.Fatalf("upsert query missing no-regression CASE guard for %q", col)
		}
	}

	// *_observed flags must be sticky (OR).
	for _, col := range []string{"tool_calls_observed", "tool_failures_observed",
		"approval_requests_observed", "token_snapshot_observed"} {
		want := "session_insights." + col + " OR EXCLUDED." + col
		if !strings.Contains(sql, want) {
			t.Fatalf("upsert query missing sticky-observed guard for %q", col)
		}
	}
}

func TestObservedFilterQueriesActuallyFilter(t *testing.T) {
	t.Parallel()
	sql := readRepoFile(t, filepath.Join("sql", "queries", "session_insight.sql"))
	if !strings.Contains(sql, "WHERE approval_requests_observed = TRUE") {
		t.Fatal("ListObservedApprovalRequests must filter on approval_requests_observed = TRUE")
	}
	if !strings.Contains(sql, "WHERE token_snapshot_observed = TRUE") {
		t.Fatal("ListObservedTokenTurns must filter on token_snapshot_observed = TRUE")
	}
}

// ----- helpers -----

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			data, err := os.ReadFile(filepath.Join(dir, rel))
			if err != nil {
				t.Fatalf("read %s: %v", rel, err)
			}
			return string(data)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("go.mod not found walking up from %s", file)
	return ""
}

// silence unused import warnings when json / time are only used via
// defaults above.
var _ = json.RawMessage(nil)
var _ = time.Time{}
