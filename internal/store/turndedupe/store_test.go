package turndedupe

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// fakeQuerier is an in-memory stand-in for *sqlc.Queries that
// satisfies the narrow querier interface used by store. Each test
// constructs one so assertions stay local.
type fakeQuerier struct {
	rows         map[string]sqlc.TurnDedupeRegistry
	upsertErr    error
	bindErr      error
	terminalErr  error
	getErr       error
	sweepErr     error
	lastUpsert   sqlc.UpsertTurnDedupeRegistryParams
	lastBind     sqlc.BindTurnDedupeProviderIDParams
	lastTerminal sqlc.MarkTurnDedupeTerminalParams
	lastCutoff   int64
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{rows: map[string]sqlc.TurnDedupeRegistry{}}
}

func (f *fakeQuerier) UpsertTurnDedupeRegistry(_ context.Context, p sqlc.UpsertTurnDedupeRegistryParams) error {
	f.lastUpsert = p
	if f.upsertErr != nil {
		return f.upsertErr
	}
	existing, ok := f.rows[p.DedupeKey]
	row := sqlc.TurnDedupeRegistry{
		DedupeKey:   p.DedupeKey,
		LocalTurnID: p.LocalTurnID,
		UpdatedAt:   p.Now,
		TerminalAt:  nil,
	}
	if !ok {
		row.CreatedAt = p.Now
		row.ThreadID = p.ThreadID
	} else {
		row.CreatedAt = existing.CreatedAt
		row.ProviderTurnID = existing.ProviderTurnID
		if p.ThreadID == "" {
			row.ThreadID = existing.ThreadID
		} else {
			row.ThreadID = p.ThreadID
		}
	}
	f.rows[p.DedupeKey] = row
	return nil
}

func (f *fakeQuerier) BindTurnDedupeProviderID(_ context.Context, p sqlc.BindTurnDedupeProviderIDParams) error {
	f.lastBind = p
	if f.bindErr != nil {
		return f.bindErr
	}
	row, ok := f.rows[p.DedupeKey]
	if !ok {
		return nil
	}
	row.ProviderTurnID = p.ProviderTurnID
	row.UpdatedAt = p.Now
	f.rows[p.DedupeKey] = row
	return nil
}

func (f *fakeQuerier) MarkTurnDedupeTerminal(_ context.Context, p sqlc.MarkTurnDedupeTerminalParams) error {
	f.lastTerminal = p
	if f.terminalErr != nil {
		return f.terminalErr
	}
	row, ok := f.rows[p.DedupeKey]
	if !ok {
		return nil
	}
	if row.TerminalAt == nil {
		row.TerminalAt = p.Now
		if p.Now != nil {
			row.UpdatedAt = *p.Now
		}
	}
	f.rows[p.DedupeKey] = row
	return nil
}

func (f *fakeQuerier) GetLiveTurnDedupe(_ context.Context, arg sqlc.GetLiveTurnDedupeParams) (sqlc.TurnDedupeRegistry, error) {
	key := arg.DedupeKey
	if f.getErr != nil {
		return sqlc.TurnDedupeRegistry{}, f.getErr
	}
	row, ok := f.rows[key]
	if !ok || row.TerminalAt != nil {
		return sqlc.TurnDedupeRegistry{}, sql.ErrNoRows
	}
	return row, nil
}

func (f *fakeQuerier) SweepTurnDedupeRegistry(_ context.Context, arg sqlc.SweepTurnDedupeRegistryParams) error {
	cutoff := arg.Cutoff
	f.lastCutoff = cutoff
	if f.sweepErr != nil {
		return f.sweepErr
	}
	for k, row := range f.rows {
		if row.UpdatedAt != 0 && row.UpdatedAt < cutoff {
			delete(f.rows, k)
		}
	}
	return nil
}

func TestStoreUpsertThenGetLiveRoundTrip(t *testing.T) {
	t.Parallel()
	q := newFakeQuerier()
	s := newStoreForTest(q)
	now := time.Unix(1_700_000_000, 0).UTC()

	if err := s.Upsert(context.Background(), UpsertParams{
		DedupeKey:   "k1",
		LocalTurnID: "turn-1",
		ThreadID:    "thread-1",
		Now:         now,
	}); err != nil {
		t.Fatalf("Upsert err = %v", err)
	}
	e, err := s.GetLive(context.Background(), "k1")
	if err != nil {
		t.Fatalf("GetLive err = %v", err)
	}
	if e.LocalTurnID != "turn-1" || e.ThreadID != "thread-1" || !e.TerminalAt.IsZero() {
		t.Fatalf("GetLive returned %+v", e)
	}
}

func TestStoreUpsertPreservesThreadOnEmpty(t *testing.T) {
	t.Parallel()
	q := newFakeQuerier()
	s := newStoreForTest(q)
	_ = s.Upsert(context.Background(), UpsertParams{DedupeKey: "k", LocalTurnID: "t", ThreadID: "original", Now: time.Now()})
	// Second upsert with empty thread id must preserve "original".
	_ = s.Upsert(context.Background(), UpsertParams{DedupeKey: "k", LocalTurnID: "t2", ThreadID: "", Now: time.Now()})

	e, err := s.GetLive(context.Background(), "k")
	if err != nil {
		t.Fatalf("GetLive err = %v", err)
	}
	if e.ThreadID != "original" {
		t.Fatalf("ThreadID not preserved on empty override, got %q", e.ThreadID)
	}
	if e.LocalTurnID != "t2" {
		t.Fatalf("LocalTurnID not overwritten, got %q", e.LocalTurnID)
	}
}

func TestStoreMarkTerminalHidesFromGetLive(t *testing.T) {
	t.Parallel()
	q := newFakeQuerier()
	s := newStoreForTest(q)
	_ = s.Upsert(context.Background(), UpsertParams{DedupeKey: "k", LocalTurnID: "t", Now: time.Now()})
	if err := s.MarkTerminal(context.Background(), "k", time.Now()); err != nil {
		t.Fatalf("MarkTerminal err = %v", err)
	}
	_, err := s.GetLive(context.Background(), "k")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound after terminal, got %v", err)
	}
}

func TestStoreGetLiveNotFound(t *testing.T) {
	t.Parallel()
	q := newFakeQuerier()
	s := newStoreForTest(q)
	_, err := s.GetLive(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	// Empty key short-circuits to ErrNotFound without hitting the querier.
	_, err = s.GetLive(context.Background(), "  ")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty key: want ErrNotFound, got %v", err)
	}
}

func TestStoreBindProviderTurnIDUpdatesRow(t *testing.T) {
	t.Parallel()
	q := newFakeQuerier()
	s := newStoreForTest(q)
	_ = s.Upsert(context.Background(), UpsertParams{DedupeKey: "k", LocalTurnID: "t", Now: time.Now()})
	if err := s.BindProviderTurnID(context.Background(), BindProviderTurnIDParams{
		DedupeKey:      "k",
		ProviderTurnID: "p-99",
		Now:            time.Now(),
	}); err != nil {
		t.Fatalf("Bind err = %v", err)
	}
	e, _ := s.GetLive(context.Background(), "k")
	if e.ProviderTurnID != "p-99" {
		t.Fatalf("provider id not bound, got %q", e.ProviderTurnID)
	}
}

func TestStoreSweepRemovesAgedRows(t *testing.T) {
	t.Parallel()
	q := newFakeQuerier()
	s := newStoreForTest(q)
	old := time.Now().Add(-time.Hour)
	_ = s.Upsert(context.Background(), UpsertParams{DedupeKey: "old", LocalTurnID: "t", Now: old})
	fresh := time.Now()
	_ = s.Upsert(context.Background(), UpsertParams{DedupeKey: "fresh", LocalTurnID: "t", Now: fresh})

	cutoff := time.Now().Add(-30 * time.Minute)
	if err := s.Sweep(context.Background(), cutoff); err != nil {
		t.Fatalf("Sweep err = %v", err)
	}
	if _, ok := q.rows["old"]; ok {
		t.Fatal("old row should have been swept")
	}
	if _, ok := q.rows["fresh"]; !ok {
		t.Fatal("fresh row must remain")
	}
}

func TestStoreUpsertRejectsEmptyKey(t *testing.T) {
	t.Parallel()
	s := newStoreForTest(newFakeQuerier())
	if err := s.Upsert(context.Background(), UpsertParams{LocalTurnID: "t"}); err == nil {
		t.Fatal("empty dedupe key must error")
	}
	if err := s.Upsert(context.Background(), UpsertParams{DedupeKey: "k"}); err == nil {
		t.Fatal("empty local turn id must error")
	}
}
