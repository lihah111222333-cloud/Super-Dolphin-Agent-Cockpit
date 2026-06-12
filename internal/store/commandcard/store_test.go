package commandcard

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5"
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
	if captured.Column1 != "roll" || captured.Limit != 25 {
		t.Fatalf("List() forwarded wrong params: %+v", captured)
	}
	if len(got) != 1 {
		t.Fatalf("List() len = %d, want 1", len(got))
	}
	assertMappedCommandCardRow(t, got[0], lastRun)
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

// ensureNotPgxErrCompat keeps jackc/pgx/v5 in the test imports so future
// tests that exercise pgx.ErrNoRows / pgconn.PgError paths can share the
// module. Without this reference the unused import would fail to build.
var _ = pgx.ErrNoRows
