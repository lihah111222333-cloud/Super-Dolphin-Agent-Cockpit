package commandcard

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	_ "modernc.org/sqlite"
)

type commandCardQuerierStub struct {
	listFn func(context.Context, sqlc.ListCommandCardsParams) ([]sqlc.ListCommandCardsRow, error)
}

func (s *commandCardQuerierStub) ListCommandCards(ctx context.Context, arg sqlc.ListCommandCardsParams) ([]sqlc.ListCommandCardsRow, error) {
	if s.listFn != nil {
		return s.listFn(ctx, arg)
	}
	return nil, nil
}

func TestListForwardsFilterAndMapsRows(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_000_000, 0).UTC()
	lastRun := now.Add(-time.Hour)
	var captured sqlc.ListCommandCardsParams

	s := &store{q: &commandCardQuerierStub{
		listFn: func(_ context.Context, arg sqlc.ListCommandCardsParams) ([]sqlc.ListCommandCardsRow, error) {
			captured = arg
			return []sqlc.ListCommandCardsRow{{
				ID:              42,
				CardKey:         "deploy.rollback",
				Title:           "Rollback deploy",
				Description:     "rollback last build",
				CommandTemplate: "make rollback",
				ArgsSchema:      []byte(`{"type":"object"}`),
				RiskLevel:       "high",
				Enabled:         int64(1),
				CreatedBy:       "ops",
				UpdatedBy:       "ops",
				CreatedAt:       now.UnixMilli(),
				UpdatedAt:       now.UnixMilli(),
				LastRunAt:       lastRun.UnixMilli(),
				RunCount:        7,
			}}, nil
		},
	}}

	got, err := s.List(context.Background(), ListFilter{Keyword: "roll", Limit: 25})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if captured.Keyword != "roll" || captured.LimitCount != 25 {
		t.Fatalf("List() forwarded wrong params: %+v", captured)
	}
	if len(got) != 1 {
		t.Fatalf("List() len = %d, want 1", len(got))
	}
	assertMappedCommandCardRow(t, got[0], lastRun)
}

func TestListKeywordMatchesRealSQLiteQuery(t *testing.T) {
	t.Parallel()

	db := openCommandCardSQLite(t)
	execCommandCardSQL(t, db, `CREATE TABLE command_cards (
		id INTEGER PRIMARY KEY,
		card_key TEXT NOT NULL,
		title TEXT NOT NULL,
		description TEXT NOT NULL,
		command_template TEXT NOT NULL,
		args_schema TEXT NOT NULL,
		risk_level TEXT NOT NULL,
		enabled INTEGER NOT NULL,
		created_by TEXT NOT NULL,
		updated_by TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);`)
	execCommandCardSQL(t, db, `CREATE TABLE command_card_runs (
		id INTEGER PRIMARY KEY,
		card_key TEXT NOT NULL,
		created_at INTEGER NOT NULL
	);`)
	now := time.Unix(1_000_000, 0).UTC().UnixMilli()
	execCommandCardSQL(t, db, `INSERT INTO command_cards (
		card_key, title, description, command_template, args_schema, risk_level,
		enabled, created_by, updated_by, created_at, updated_at
	) VALUES
		('deploy.rollback', 'Rollback deploy', 'rollback last build', 'make rollback', '{}', 'high', 1, 'ops', 'ops', ?, ?),
		('deploy.status', 'Status deploy', 'show status', 'make status', '{}', 'low', 1, 'ops', 'ops', ?, ?);`, now, now, now-1, now-1)

	s := &store{q: sqlc.New(db)}
	got, err := s.List(context.Background(), ListFilter{Keyword: "rollback", Limit: 10})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].CardKey != "deploy.rollback" {
		t.Fatalf("List() keyword result = %+v, want only deploy.rollback", got)
	}
}

func assertMappedCommandCardRow(t *testing.T, card CommandCard, lastRun time.Time) {
	t.Helper()
	if card.ID != 42 || card.CardKey != "deploy.rollback" || card.RiskLevel != "high" || !card.Enabled || card.RunCount != 7 {
		t.Fatalf("List() row mapped incorrectly: %+v", card)
	}
	if card.LastRunAt == nil || !card.LastRunAt.Equal(lastRun) {
		t.Fatalf("List() LastRunAt = %v, want %v", card.LastRunAt, lastRun)
	}
	if string(card.ArgsSchema) != `{"type":"object"}` {
		t.Fatalf("List() ArgsSchema = %s", card.ArgsSchema)
	}
}

func TestListReturnsEmptySliceWhenNoRows(t *testing.T) {
	t.Parallel()
	s := &store{q: &commandCardQuerierStub{
		listFn: func(context.Context, sqlc.ListCommandCardsParams) ([]sqlc.ListCommandCardsRow, error) {
			return nil, nil
		},
	}}
	got, err := s.List(context.Background(), ListFilter{})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("List() returned nil, expected non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("List() len = %d, want 0", len(got))
	}
}

func TestListWrapsQuerierError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("database down")
	s := &store{q: &commandCardQuerierStub{
		listFn: func(context.Context, sqlc.ListCommandCardsParams) ([]sqlc.ListCommandCardsRow, error) {
			return nil, sentinel
		},
	}}
	_, err := s.List(context.Background(), ListFilter{})
	if err == nil {
		t.Fatal("List() expected wrapped error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("List() error = %v, want wrap of sentinel", err)
	}
}

func TestListMapsNilLastRunAt(t *testing.T) {
	t.Parallel()
	s := &store{q: &commandCardQuerierStub{
		listFn: func(context.Context, sqlc.ListCommandCardsParams) ([]sqlc.ListCommandCardsRow, error) {
			return []sqlc.ListCommandCardsRow{{
				ID:        1,
				CardKey:   "noop",
				LastRunAt: nil,
			}}, nil
		},
	}}
	got, err := s.List(context.Background(), ListFilter{})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].LastRunAt != nil {
		t.Fatalf("List() LastRunAt = %v, want nil", got[0].LastRunAt)
	}
}

func openCommandCardSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	})
	return db
}

func execCommandCardSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec sql: %v\n%s", err, query)
	}
}
