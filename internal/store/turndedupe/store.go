package turndedupe

import (
	"context"
	"errors"
	"strings"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// querier is the narrow subset of *sqlc.Queries this store actually
// calls. Splitting it out keeps unit tests free of a live pool — a
// test fake implements only what is exercised.
type querier interface {
	UpsertTurnDedupeRegistry(ctx context.Context, arg sqlc.UpsertTurnDedupeRegistryParams) error
	BindTurnDedupeProviderID(ctx context.Context, arg sqlc.BindTurnDedupeProviderIDParams) error
	MarkTurnDedupeTerminal(ctx context.Context, arg sqlc.MarkTurnDedupeTerminalParams) error
	GetLiveTurnDedupe(ctx context.Context, arg sqlc.GetLiveTurnDedupeParams) (sqlc.TurnDedupeRegistry, error)
	SweepTurnDedupeRegistry(ctx context.Context, arg sqlc.SweepTurnDedupeRegistryParams) error
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
		Now:         tsMS(nonZero(p.Now)),
	})
}

func (s *store) BindProviderTurnID(ctx context.Context, p BindProviderTurnIDParams) error {
	key := strings.TrimSpace(p.DedupeKey)
	if key == "" {
		return errors.New("turndedupe: dedupe key required for bind")
	}
	return s.q.BindTurnDedupeProviderID(ctx, sqlc.BindTurnDedupeProviderIDParams{
		ProviderTurnID: strings.TrimSpace(p.ProviderTurnID),
		Now:            tsMS(nonZero(p.Now)),
		DedupeKey:      key,
	})
}

func (s *store) MarkTerminal(ctx context.Context, dedupeKey string, now time.Time) error {
	key := strings.TrimSpace(dedupeKey)
	if key == "" {
		return errors.New("turndedupe: dedupe key required for mark terminal")
	}
	v := tsMS(nonZero(now))
	return s.q.MarkTurnDedupeTerminal(ctx, sqlc.MarkTurnDedupeTerminalParams{
		Now:       &v,
		DedupeKey: key,
	})
}

func (s *store) GetLive(ctx context.Context, dedupeKey string) (Entry, error) {
	key := strings.TrimSpace(dedupeKey)
	if key == "" {
		return Entry{}, ErrNotFound
	}
	row, err := s.q.GetLiveTurnDedupe(ctx, sqlc.GetLiveTurnDedupeParams{DedupeKey: key})
	if err != nil {
		if platformdb.IsNotFound(err) {
			return Entry{}, ErrNotFound
		}
		return Entry{}, platformdb.WrapStoreError(err, "get_live", "turn_dedupe_registry")
	}
	return Entry{
		DedupeKey:      row.DedupeKey,
		LocalTurnID:    row.LocalTurnID,
		ProviderTurnID: row.ProviderTurnID,
		ThreadID:       row.ThreadID,
		CreatedAt:      fromMS(row.CreatedAt),
		UpdatedAt:      fromMS(row.UpdatedAt),
		TerminalAt:     fromMSPtr(row.TerminalAt),
	}, nil
}

func (s *store) Sweep(ctx context.Context, cutoff time.Time) error {
	if cutoff.IsZero() {
		return errors.New("turndedupe: sweep cutoff must be non-zero")
	}
	return platformdb.WrapStoreError(
		s.q.SweepTurnDedupeRegistry(ctx, sqlc.SweepTurnDedupeRegistryParams{Cutoff: tsMS(cutoff)}),
		"sweep",
		"turn_dedupe_registry",
	)
}

// nonZero returns now when t is zero, otherwise t.
func nonZero(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}

func tsMS(t time.Time) int64 {
	return platformdb.Millis(t)
}

func fromMS(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return platformdb.TimeFromMillis(ms)
}

func fromMSPtr(ms *int64) time.Time {
	if ms == nil {
		return time.Time{}
	}
	return fromMS(*ms)
}
