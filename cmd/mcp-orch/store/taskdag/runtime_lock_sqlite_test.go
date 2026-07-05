package taskdag_test

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/fxadapter"
	orchcron "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/cron"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	sqliteruntime "github.com/anthropic-ai/super-agent-v3/internal/platform/db/sqlite"
	_ "modernc.org/sqlite"
)

func TestSQLiteRuntimeLockHolderRenewAndReleaseAreFenced(t *testing.T) {
	ctx := context.Background()
	db := openRuntimeLockSQLiteDB(t)
	locker, err := fxadapter.NewSQLiteRuntimeLocker(db, "task12-runtime-lock")
	if err != nil {
		t.Fatalf("NewSQLiteRuntimeLocker() error = %v", err)
	}
	handle, acquired, err := locker.TryLock(ctx)
	if err != nil {
		t.Fatalf("TryLock() error = %v", err)
	}
	if !acquired {
		t.Fatal("TryLock() acquired=false, want true")
	}
	holder := sqliteRuntimeLockHolder(t, ctx, db, "task12-runtime-lock")
	if parts := strings.Split(holder, ":"); len(parts) < 3 {
		t.Fatalf("runtime lock holder = %q, want hostname:pid:process-start-nonce", holder)
	}
	if err := handle.Renew(ctx); err != nil {
		t.Fatalf("Renew() by holder error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE runtime_locks SET holder = 'other-holder' WHERE lock_key = ?`, "task12-runtime-lock"); err != nil {
		t.Fatalf("steal runtime lock holder: %v", err)
	}
	if err := handle.Renew(ctx); err == nil {
		t.Fatalf("Renew() after holder mismatch err=nil, want fence error")
	}
	if err := handle.Unlock(ctx); err == nil {
		t.Fatalf("Unlock() after holder mismatch err=nil, want fence error")
	}
}

func TestSQLiteRuntimeLockAllowsOnlyOneScheduledTickerAcrossConnections(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runtime-lock.sqlite")
	dbA := openRuntimeLockSQLiteDBAt(t, dbPath, 4)
	dbB := openRuntimeLockSQLiteDBAt(t, dbPath, 4)
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	seedSQLiteScheduledDAG(t, ctx, dbA, "scheduled-dag", now.Add(-time.Minute))

	starter := newBlockingRuntimeLockStarter()
	releasedStarter := false
	releaseStarter := func() {
		if !releasedStarter {
			close(starter.release)
			releasedStarter = true
		}
	}
	defer releaseStarter()
	tickerA := newSQLiteScheduledTicker(t, dbA, "task12-shared-scheduled-lock", starter)
	tickerB := newSQLiteScheduledTicker(t, dbB, "task12-shared-scheduled-lock", &recordingRuntimeLockStarter{})

	firstDone := make(chan tickResult, 1)
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Go(func() {
		n, err := tickerA.Tick(ctx, now)
		firstDone <- tickResult{n: n, err: err}
	})
	<-starter.entered
	n, err := tickerB.Tick(ctx, now)
	if err != nil {
		t.Fatalf("second Tick() error = %v", err)
	}
	if n != 0 {
		t.Fatalf("second Tick() triggered = %d, want 0 while first holder owns lock", n)
	}
	releaseStarter()
	first := <-firstDone
	if first.err != nil {
		t.Fatalf("first Tick() error = %v", first.err)
	}
	if first.n != 1 {
		t.Fatalf("first Tick() triggered = %d, want 1", first.n)
	}
	if starter.calls() != 1 {
		t.Fatalf("StartDAG calls = %d, want 1", starter.calls())
	}
}

type tickResult struct {
	n   int
	err error
}

type blockingRuntimeLockStarter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	count   int
}

func newBlockingRuntimeLockStarter() *blockingRuntimeLockStarter {
	return &blockingRuntimeLockStarter{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (s *blockingRuntimeLockStarter) StartDAG(ctx context.Context, req orchcron.ScheduledDAGStartRequest) error {
	s.once.Do(func() { close(s.entered) })
	s.mu.Lock()
	s.count++
	s.mu.Unlock()
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *blockingRuntimeLockStarter) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

func newSQLiteScheduledTicker(t *testing.T, db *sql.DB, lockKey string, starter orchcron.DAGStarter) *orchcron.ScheduledDAGTicker {
	t.Helper()
	scheduleStore, err := fxadapter.NewSQLDAGScheduleStore(sqlc.New(db))
	if err != nil {
		t.Fatalf("NewSQLDAGScheduleStore() error = %v", err)
	}
	locker, err := fxadapter.NewSQLiteRuntimeLocker(db, lockKey)
	if err != nil {
		t.Fatalf("NewSQLiteRuntimeLocker() error = %v", err)
	}
	ticker, err := orchcron.NewScheduledDAGTicker(orchcron.ScheduledDAGTickerConfig{
		Store:   scheduleStore,
		Starter: starter,
		Locker:  locker,
	})
	if err != nil {
		t.Fatalf("NewScheduledDAGTicker() error = %v", err)
	}
	return ticker
}

type recordingRuntimeLockStarter struct{}

func (*recordingRuntimeLockStarter) StartDAG(context.Context, orchcron.ScheduledDAGStartRequest) error {
	return nil
}

func seedSQLiteScheduledDAG(t *testing.T, ctx context.Context, db *sql.DB, dagKey string, dueAt time.Time) {
	t.Helper()
	nowMillis := time.Now().UTC().UnixMilli()
	_, err := db.ExecContext(ctx, `
INSERT INTO task_dags (dag_key, title, description, status, created_by, metadata, created_at, updated_at, trigger, cron_expr, next_run_at)
VALUES (?, ?, '', 'draft', 'tester', '{}', ?, ?, 'scheduled', '* * * * *', ?)`,
		dagKey, dagKey, nowMillis, nowMillis, dueAt.UTC().UnixMilli(),
	)
	if err != nil {
		t.Fatalf("insert scheduled dag: %v", err)
	}
}

func sqliteRuntimeLockHolder(t *testing.T, ctx context.Context, db *sql.DB, key string) string {
	t.Helper()
	var holder string
	if err := db.QueryRowContext(ctx, `SELECT holder FROM runtime_locks WHERE lock_key = ?`, key).Scan(&holder); err != nil {
		t.Fatalf("load runtime lock holder: %v", err)
	}
	return holder
}

func openRuntimeLockSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	return openRuntimeLockSQLiteDBAt(t, filepath.Join(t.TempDir(), "runtime-lock.sqlite"), 4)
}

func openRuntimeLockSQLiteDBAt(t *testing.T, path string, maxOpenConns int) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", runtimeLockSQLiteDSN(path))
	if err != nil {
		t.Fatalf("open sqlite %s: %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(maxOpenConns)
	if err := sqliteruntime.RunMigrations(context.Background(), db, runtimeLockSQLiteMigrationsDir(t)); err != nil {
		t.Fatalf("run sqlite migrations: %v", err)
	}
	return db
}

func runtimeLockSQLiteDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout=5000")
	q.Add("_pragma", "foreign_keys=ON")
	q.Add("_pragma", "journal_mode=WAL")
	return path + "?" + q.Encode()
}

func runtimeLockSQLiteMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "internal", "platform", "db", "sqlite", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	return dir
}
