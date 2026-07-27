package thread

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
	_ "modernc.org/sqlite"
)

func TestArchiveStateTransactionRollsBackEitherWriteFailure(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		trigger string
	}{
		{
			name: "thread write fails",
			trigger: `CREATE TRIGGER fail_thread_archive BEFORE UPDATE ON agent_threads
				BEGIN SELECT RAISE(ABORT, 'injected thread failure'); END;`,
		},
		{
			name: "binding write fails",
			trigger: `CREATE TRIGGER fail_binding_archive BEFORE UPDATE ON agent_provider_binding
				BEGIN SELECT RAISE(ABORT, 'injected binding failure'); END;`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db := openArchiveStateTestDB(t)
			if _, err := db.Exec(testCase.trigger); err != nil {
				t.Fatalf("create failure trigger: %v", err)
			}
			store := NewStoreWithDB(db, sqlc.New(db)).(ArchiveStateStore)
			err := store.SetArchiveState(context.Background(), ArchiveStateParams{
				ThreadID: "thread-1", AgentID: "agent-1", Archived: true, UpdatedAt: 99,
			})
			if err == nil {
				t.Fatal("SetArchiveState() error = nil, want injected write failure")
			}
			assertArchiveStateRows(t, db, "created", 0, 1)
		})
	}
}

func TestArchiveStateTransactionCommitsBothRows(t *testing.T) {
	t.Parallel()

	db := openArchiveStateTestDB(t)
	store := NewStoreWithDB(db, sqlc.New(db)).(ArchiveStateStore)
	ctx := context.Background()
	if err := store.SetArchiveState(ctx, ArchiveStateParams{
		ThreadID: "thread-1", AgentID: "agent-1", Archived: true, UpdatedAt: 99,
	}); err != nil {
		t.Fatalf("SetArchiveState(archive) error = %v", err)
	}
	assertArchiveStateRows(t, db, "archived", 1, 99)
	if err := store.SetArchiveState(ctx, ArchiveStateParams{
		ThreadID: "thread-1", AgentID: "agent-1", Archived: false, UpdatedAt: 101,
	}); err != nil {
		t.Fatalf("SetArchiveState(unarchive) error = %v", err)
	}
	assertArchiveStateRows(t, db, "created", 0, 101)
}

func openArchiveStateTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "archive-state.db"))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE agent_threads (
			thread_id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE agent_provider_binding (
			agent_id TEXT PRIMARY KEY,
			archived INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		INSERT INTO agent_threads(thread_id, status, updated_at) VALUES ('thread-1', 'created', 1);
		INSERT INTO agent_provider_binding(agent_id, archived, updated_at) VALUES ('agent-1', 0, 1);
	`); err != nil {
		t.Fatalf("seed archive state schema: %v", err)
	}
	return db
}

func assertArchiveStateRows(t *testing.T, db *sql.DB, wantStatus string, wantArchived, wantUpdatedAt int64) {
	t.Helper()
	var status string
	var threadUpdatedAt int64
	if err := db.QueryRow(`SELECT status, updated_at FROM agent_threads WHERE thread_id = 'thread-1'`).
		Scan(&status, &threadUpdatedAt); err != nil {
		t.Fatalf("read thread archive state: %v", err)
	}
	var archived, bindingUpdatedAt int64
	if err := db.QueryRow(`SELECT archived, updated_at FROM agent_provider_binding WHERE agent_id = 'agent-1'`).
		Scan(&archived, &bindingUpdatedAt); err != nil {
		t.Fatalf("read binding archive state: %v", err)
	}
	if status != wantStatus || archived != wantArchived ||
		threadUpdatedAt != wantUpdatedAt || bindingUpdatedAt != wantUpdatedAt {
		t.Fatalf(
			"archive state = (%q,%d,%d,%d), want (%q,%d,%d,%d)",
			status, archived, threadUpdatedAt, bindingUpdatedAt,
			wantStatus, wantArchived, wantUpdatedAt, wantUpdatedAt,
		)
	}
}
