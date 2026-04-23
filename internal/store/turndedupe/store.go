package turndedupe

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// querier is the narrow subset of *sqlc.Queries this store actually
// calls. Splitting it out keeps unit tests free of a live pool — a
// test fake implements only what is exercised.
type querier interface {
	UpsertTurnDedupeRegistry(ctx context.Context, arg sqlc.UpsertTurnDedupeRegistryParams) error
	BindTurnDedupeProviderID(ctx context.Context, arg sqlc.BindTurnDedupeProviderIDParams) error
	MarkTurnDedupeTerminal(ctx context.Context, arg sqlc.MarkTurnDedupeTerminalParams) error
	GetLiveTurnDedupe(ctx context.Context, dedupeKey string) (sqlc.TurnDedupeRegistry, error)
	SweepTurnDedupeRegistry(ctx context.Context, cutoff pgtype.Timestamptz) error
}

type store struct {
	q querier
}

// NewStore wires the production sqlc-backed Store. Pool injection
// happens at the fx layer via *sqlc.Queries.
func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

// newStoreForTest exists so tests can plug in a fake querier without
// exporting the internal struct.
func newStoreForTest(q querier) Store { return &store{q: q} }

func (s *store) Upsert(ctx context.Context, p UpsertParams) error {
	key := strings.TrimSpace(p.DedupeKey)
	if key == "" {
		return errors.New("turndedupe: dedupe key required for upsert")
	}
	if strings.TrimSpace(p.LocalTurnID) == "" {
		return errors.New("turndedupe: local turn id required for upsert")
	}
	return s.q.UpsertTurnDedupeRegistry(ctx, sqlc.UpsertTurnDedupeRegistryParams{
		DedupeKey:   key,
		LocalTurnID: strings.TrimSpace(p.LocalTurnID),
		ThreadID:    strings.TrimSpace(p.ThreadID),
		Now:         ts(nonZero(p.Now)),
	})
}

func (s *store) BindProviderTurnID(ctx context.Context, p BindProviderTurnIDParams) error {
	key := strings.TrimSpace(p.DedupeKey)
	if key == "" {
		return errors.New("turndedupe: dedupe key required for bind")
	}
	return s.q.BindTurnDedupeProviderID(ctx, sqlc.BindTurnDedupeProviderIDParams{
		ProviderTurnID: strings.TrimSpace(p.ProviderTurnID),
		Now:            ts(nonZero(p.Now)),
		DedupeKey:      key,
	})
}

func (s *store) MarkTerminal(ctx context.Context, dedupeKey string, now time.Time) error {
	key := strings.TrimSpace(dedupeKey)
	if key == "" {
		return errors.New("turndedupe: dedupe key required for mark terminal")
	}
	return s.q.MarkTurnDedupeTerminal(ctx, sqlc.MarkTurnDedupeTerminalParams{
		Now:       ts(nonZero(now)),
		DedupeKey: key,
	})
}

func (s *store) GetLive(ctx context.Context, dedupeKey string) (Entry, error) {
	key := strings.TrimSpace(dedupeKey)
	if key == "" {
		return Entry{}, ErrNotFound
	}
	row, err := s.q.GetLiveTurnDedupe(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Entry{}, ErrNotFound
		}
		return Entry{}, err
	}
	return Entry{
		DedupeKey:      row.DedupeKey,
		LocalTurnID:    row.LocalTurnID,
		ProviderTurnID: row.ProviderTurnID,
		ThreadID:       row.ThreadID,
		CreatedAt:      fromTS(row.CreatedAt),
		UpdatedAt:      fromTS(row.UpdatedAt),
		TerminalAt:     fromTS(row.TerminalAt),
	}, nil
}

func (s *store) Sweep(ctx context.Context, cutoff time.Time) error {
	if cutoff.IsZero() {
		return errors.New("turndedupe: sweep cutoff must be non-zero")
	}
	return s.q.SweepTurnDedupeRegistry(ctx, ts(cutoff))
}

// nonZero returns now when t is zero, otherwise t. Guards against a
// caller forgetting to stamp time.Now() into the params.
func nonZero(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}

func ts(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func fromTS(p pgtype.Timestamptz) time.Time {
	if !p.Valid {
		return time.Time{}
	}
	return p.Time
}
