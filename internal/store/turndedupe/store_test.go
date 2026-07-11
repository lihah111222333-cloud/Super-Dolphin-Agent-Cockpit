package turndedupe

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
	_ "modernc.org/sqlite"
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
	if isTerminalDedupeRow(existing, ok) {
		return nil
	}
	f.rows[p.DedupeKey] = upsertFakeDedupeRow(existing, ok, p)
	return nil
}

func isTerminalDedupeRow(row sqlc.TurnDedupeRegistry, ok bool) bool {
	return ok && row.TerminalAt != nil
}

func upsertFakeDedupeRow(existing sqlc.TurnDedupeRegistry, ok bool, p sqlc.UpsertTurnDedupeRegistryParams) sqlc.TurnDedupeRegistry {
	if !ok {
		return newFakeDedupeRow(p)
	}
	row := existing
	row.LocalTurnID = p.LocalTurnID
	row.UpdatedAt = p.Now
	row.TerminalAt = nil
	if p.ThreadID != "" {
		row.ThreadID = p.ThreadID
	}
	return row
}

func newFakeDedupeRow(p sqlc.UpsertTurnDedupeRegistryParams) sqlc.TurnDedupeRegistry {
	return sqlc.TurnDedupeRegistry{
		DedupeKey:   p.DedupeKey,
		LocalTurnID: p.LocalTurnID,
		ThreadID:    p.ThreadID,
		CreatedAt:   p.Now,
		UpdatedAt:   p.Now,
		TerminalAt:  nil,
	}
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

func TestStoreTerminalRecordCannotBeRevivedByUpsert(t *testing.T) {
	t.Parallel()
	q := newFakeQuerier()
	s := newStoreForTest(q)
	base := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	_ = s.Upsert(context.Background(), UpsertParams{DedupeKey: "k", LocalTurnID: "t1", ThreadID: "thread-1", Now: base})
	if err := s.MarkTerminal(context.Background(), "k", base.Add(time.Minute)); err != nil {
		t.Fatalf("MarkTerminal err = %v", err)
	}
	if err := s.Upsert(context.Background(), UpsertParams{DedupeKey: "k", LocalTurnID: "t2", ThreadID: "thread-2", Now: base.Add(2 * time.Minute)}); err != nil {
		t.Fatalf("Upsert after terminal err = %v", err)
	}

	_, err := s.GetLive(context.Background(), "k")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetLive after terminal upsert err = %v, want ErrNotFound", err)
	}
	row := q.rows["k"]
	if row.LocalTurnID != "t1" || row.ThreadID != "thread-1" || row.TerminalAt == nil {
		t.Fatalf("terminal row revived or overwritten: %+v", row)
	}
}

func TestSQLiteTerminalRecordCannotBeRevivedByUpsert(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
CREATE TABLE turn_dedupe_registry (
	dedupe_key TEXT PRIMARY KEY,
	local_turn_id TEXT NOT NULL,
	provider_turn_id TEXT NOT NULL DEFAULT '',
	thread_id TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	terminal_at INTEGER
);
`); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	s := NewStore(sqlc.New(db))
	base := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	if err := s.Upsert(context.Background(), UpsertParams{DedupeKey: "k", LocalTurnID: "t1", ThreadID: "thread-1", Now: base}); err != nil {
		t.Fatalf("Upsert initial err = %v", err)
	}
	if err := s.MarkTerminal(context.Background(), "k", base.Add(time.Minute)); err != nil {
		t.Fatalf("MarkTerminal err = %v", err)
	}
	if err := s.Upsert(context.Background(), UpsertParams{DedupeKey: "k", LocalTurnID: "t2", ThreadID: "thread-2", Now: base.Add(2 * time.Minute)}); err != nil {
		t.Fatalf("Upsert after terminal err = %v", err)
	}

	_, err = s.GetLive(context.Background(), "k")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetLive after terminal upsert err = %v, want ErrNotFound", err)
	}
	assertSQLiteTerminalRow(t, db, "k", "t1", "thread-1")
}

func assertSQLiteTerminalRow(t *testing.T, db *sql.DB, dedupeKey, wantLocalTurnID, wantThreadID string) {
	t.Helper()

	var localTurnID, threadID string
	var terminalAt sql.NullInt64
	if err := db.QueryRow(`SELECT local_turn_id, thread_id, terminal_at FROM turn_dedupe_registry WHERE dedupe_key = ?`, dedupeKey).Scan(&localTurnID, &threadID, &terminalAt); err != nil {
		t.Fatalf("query terminal row: %v", err)
	}
	assertDedupeString(t, "local_turn_id", localTurnID, wantLocalTurnID)
	assertDedupeString(t, "thread_id", threadID, wantThreadID)
	assertDedupeBool(t, "terminal_at.Valid", terminalAt.Valid, true)
}

func assertDedupeString(t *testing.T, name, got, want string) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}

func assertDedupeBool(t *testing.T, name string, got, want bool) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %v, want %v", name, got, want)
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
