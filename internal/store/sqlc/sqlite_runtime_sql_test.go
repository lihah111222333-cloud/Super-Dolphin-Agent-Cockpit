package sqlc

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestGeneratedRuntimeSQLHasNoSqlcMacros(t *testing.T) {
	t.Parallel()

	queries := map[string]string{
		"upsertAgentThread":            upsertAgentThread,
		"updateWorkspaceRunStatus":     updateWorkspaceRunStatus,
		"transitionWorkspaceRunStatus": transitionWorkspaceRunStatus,
	}
	for name, query := range queries {
		if strings.Contains(query, "sqlc.arg") || strings.Contains(query, "sqlc.narg") {
			t.Fatalf("%s contains sqlc macro at runtime: %s", name, query)
		}
	}
}

func TestSQLiteUpsertAgentThreadExecutes(t *testing.T) {
	t.Parallel()

	db := openSQLCTestSQLiteDB(t)
	q := New(db)
	ctx := context.Background()
	params := UpsertAgentThreadParams{
		ThreadID:         "thread-sqlite",
		Name:             "first",
		Prompt:           "hello",
		Model:            "gpt-5.5",
		CWD:              "C:/repo",
		Status:           "created",
		CreatedAt:        1,
		UpdatedAt:        1,
		AgentType:        "root",
		AgentMemoryScope: "project",
		ConfigOverride:   []byte(`{"mode":"first"}`),
		AgentKey:         "codex",
	}
	if err := q.UpsertAgentThread(ctx, params); err != nil {
		t.Fatalf("UpsertAgentThread(insert) error = %v", err)
	}
	params.Name = "second"
	params.UpdatedAt = 2
	params.ConfigOverride = nil
	params.PendingLaunch = 1
	if err := q.UpsertAgentThread(ctx, params); err != nil {
		t.Fatalf("UpsertAgentThread(update) error = %v", err)
	}
	row, err := q.GetAgentThreadByID(ctx, GetAgentThreadByIDParams{ThreadID: params.ThreadID})
	if err != nil {
		t.Fatalf("GetAgentThreadByID() error = %v", err)
	}
	if row.Name != "second" || row.ConfigOverride != "{}" || row.PendingLaunch != 1 {
		t.Fatalf("thread row = %+v, want updated row with default config and pending launch", row)
	}
}

func TestAgentThreadPageZeroTimestampCursorDoesNotRestartFirstPage(t *testing.T) {
	t.Parallel()

	db := openSQLCTestSQLiteDB(t)
	q := New(db)
	ctx := context.Background()
	for _, row := range []UpsertAgentThreadParams{
		{ThreadID: "thread-new", Name: "new", Status: "created", CreatedAt: 1, UpdatedAt: 1},
		{ThreadID: "thread-zero-z", Name: "zero-z", Status: "created", CreatedAt: 0, UpdatedAt: 1},
		{ThreadID: "thread-zero-y", Name: "zero-y", Status: "created", CreatedAt: 0, UpdatedAt: 1},
	} {
		if err := q.UpsertAgentThread(ctx, row); err != nil {
			t.Fatalf("UpsertAgentThread(%s) error = %v", row.ThreadID, err)
		}
	}

	first, err := q.ListAgentThreadsPage(ctx, ListAgentThreadsPageParams{
		CursorCreatedAt: int64(0),
		CursorThreadID:  "",
		Limit:           int64(2),
	})
	if err != nil {
		t.Fatalf("ListAgentThreadsPage(first) error = %v", err)
	}
	if len(first) != 3 || first[0].ThreadID != "thread-new" || first[1].ThreadID != "thread-zero-z" {
		t.Fatalf("first page plus lookahead = %+v, want thread-new then thread-zero-z", first)
	}

	second, err := q.ListAgentThreadsPage(ctx, ListAgentThreadsPageParams{
		CursorCreatedAt: first[1].CreatedAt,
		CursorThreadID:  first[1].ThreadID,
		Limit:           int64(2),
	})
	if err != nil {
		t.Fatalf("ListAgentThreadsPage(second) error = %v", err)
	}
	if len(second) != 1 || second[0].ThreadID != "thread-zero-y" {
		t.Fatalf("second page = %+v, want only thread-zero-y", second)
	}
}

func TestLoadedAgentThreadPageZeroTimestampCursorDoesNotRestartFirstPage(t *testing.T) {
	t.Parallel()

	db := openSQLCTestSQLiteDB(t)
	q := New(db)
	ctx := context.Background()
	for _, row := range []UpsertAgentThreadParams{
		{ThreadID: "thread-new", Name: "new", Status: "created", CreatedAt: 1, UpdatedAt: 1},
		{ThreadID: "thread-zero-z", Name: "zero-z", Status: "created", CreatedAt: 0, UpdatedAt: 1},
		{ThreadID: "thread-zero-y", Name: "zero-y", Status: "created", CreatedAt: 0, UpdatedAt: 1},
	} {
		if err := q.UpsertAgentThread(ctx, row); err != nil {
			t.Fatalf("UpsertAgentThread(%s) error = %v", row.ThreadID, err)
		}
	}
	first, err := q.ListLoadedAgentThreadsPage(ctx, ListLoadedAgentThreadsPageParams{
		CursorCreatedAt: 0, CursorThreadID: "", Limit: 2,
	})
	if err != nil {
		t.Fatalf("ListLoadedAgentThreadsPage(first) error = %v", err)
	}
	second, err := q.ListLoadedAgentThreadsPage(ctx, ListLoadedAgentThreadsPageParams{
		CursorCreatedAt: first[1].CreatedAt, CursorThreadID: first[1].ThreadID, Limit: 2,
	})
	if err != nil {
		t.Fatalf("ListLoadedAgentThreadsPage(second) error = %v", err)
	}
	if len(second) != 1 || second[0].ThreadID != "thread-zero-y" {
		t.Fatalf("loaded second page = %+v, want only thread-zero-y", second)
	}
}

func openSQLCTestSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	body, err := os.ReadFile(filepath.Join("..", "..", "platform", "db", "sqlite", "migrations", "001_baseline.sql"))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatalf("exec baseline: %v", err)
	}
	return db
}
