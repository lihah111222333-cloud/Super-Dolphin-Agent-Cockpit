package fxadapter

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	_ "modernc.org/sqlite"
)

func TestSQLDAGScheduleStore_DueDAGsFiltersScheduledTrigger(t *testing.T) {
	t.Parallel()

	db := newSQLiteCronTestDB(t)
	now := time.Unix(1700000000, 0).UTC()
	insertTaskDAG(t, db, "manual-dag", "manual", "", now.Add(-time.Minute))
	insertTaskDAG(t, db, "future-dag", "scheduled", "* * * * *", now.Add(time.Minute))
	insertTaskDAG(t, db, "due-dag", "scheduled", "* * * * *", now.Add(-time.Minute))

	store, err := NewSQLDAGScheduleStore(sqlc.New(db))
	if err != nil {
		t.Fatalf("NewSQLDAGScheduleStore() error = %v", err)
	}
	due, err := store.DueDAGs(context.Background(), now)
	if err != nil {
		t.Fatalf("DueDAGs() error = %v", err)
	}
	if len(due) != 1 || due[0].DagKey != "due-dag" {
		t.Fatalf("DueDAGs() = %#v, want only due-dag", due)
	}
}

func TestSQLiteRuntimeLocker_LockAndUnlock(t *testing.T) {
	t.Parallel()

	db := newSQLiteCronTestDB(t)
	locker, err := NewSQLiteRuntimeLocker(db, "test-lock")
	if err != nil {
		t.Fatalf("NewSQLiteRuntimeLocker() error = %v", err)
	}
	handle, ok, err := locker.TryLock(context.Background())
	if err != nil {
		t.Fatalf("TryLock() error = %v", err)
	}
	if !ok {
		t.Fatal("TryLock() ok = false, want true")
	}
	if err := handle.Unlock(context.Background()); err != nil {
		t.Fatalf("Unlock() error = %v", err)
	}
}

func newSQLiteCronTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE task_dags (
			id INTEGER PRIMARY KEY,
			dag_key TEXT NOT NULL UNIQUE,
			cron_expr TEXT NOT NULL DEFAULT '',
			trigger TEXT NOT NULL DEFAULT 'manual',
			next_run_at INTEGER,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE runtime_locks (
			lock_key TEXT PRIMARY KEY,
			holder TEXT NOT NULL,
			lease_expires_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
	} {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("create test table: %v", err)
		}
	}
	return db
}

func insertTaskDAG(t *testing.T, db *sql.DB, key, trigger, cronExpr string, nextRunAt time.Time) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO task_dags (dag_key, trigger, cron_expr, next_run_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		key,
		trigger,
		cronExpr,
		nextRunAt.UnixMilli(),
		time.Now().UTC().UnixMilli(),
	)
	if err != nil {
		t.Fatalf("insert task_dags %s: %v", key, err)
	}
}
