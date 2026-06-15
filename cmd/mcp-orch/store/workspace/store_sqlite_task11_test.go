package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	sqliteruntime "github.com/anthropic-ai/super-agent-v3/internal/platform/db/sqlite"
	_ "modernc.org/sqlite"
)

func TestSQLiteWorkspaceCRUDListGetUpsertAndTransition(t *testing.T) {
	ctx := context.Background()
	db := openWorkspaceSQLiteDB(t)
	store := NewStore(db)

	run := seedSQLiteWorkspaceRun(t, ctx, store)
	assertSQLiteWorkspaceRunReadPaths(t, ctx, store)
	assertSQLiteWorkspaceRunUpdate(t, ctx, store)
	assertSQLiteWorkspaceRunUpsert(t, ctx, store, run.ID)
	assertSQLiteWorkspaceFileCRUD(t, ctx, store)
}

func seedSQLiteWorkspaceRun(t *testing.T, ctx context.Context, store Store) *WorkspaceRun {
	t.Helper()
	run, err := store.UpsertRun(ctx, WorkspaceRun{
		RunKey:        "workspace-crud",
		DagKey:        "dag-crud",
		SourceRoot:    "C:/repo",
		WorkspacePath: "C:/tmp/workspace-crud",
		Status:        "active",
		CreatedBy:     "tester",
		UpdatedBy:     "tester",
		Metadata:      json.RawMessage(`{"phase":"created"}`),
	})
	if err != nil {
		t.Fatalf("UpsertRun(create) error = %v", err)
	}
	if run.ID == 0 || run.Status != "active" {
		t.Fatalf("UpsertRun(create) = %#v, want active with id", run)
	}
	return run
}

func assertSQLiteWorkspaceRunReadPaths(t *testing.T, ctx context.Context, store Store) {
	t.Helper()
	got, err := store.GetRun(ctx, "workspace-crud")
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if got.DagKey != "dag-crud" || string(got.Metadata) != `{"phase":"created"}` {
		t.Fatalf("GetRun() = %#v, want dag-crud and created metadata", got)
	}
	listed, err := store.ListRuns(ctx, ListRunsFilter{Status: "active", DagKey: "dag-crud", Limit: 10})
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(listed) != 1 || listed[0].RunKey != "workspace-crud" {
		t.Fatalf("ListRuns() = %#v, want workspace-crud", listed)
	}
}

func assertSQLiteWorkspaceRunUpdate(t *testing.T, ctx context.Context, store Store) {
	t.Helper()
	updated, err := store.UpdateRunStatus(ctx, UpdateRunStatusInput{
		RunKey:    "workspace-crud",
		Status:    "reviewing",
		UpdatedBy: "reviewer",
		Metadata:  json.RawMessage(`{"phase":"review"}`),
	})
	if err != nil {
		t.Fatalf("UpdateRunStatus() error = %v", err)
	}
	if updated.Status != "reviewing" || updated.FinishedAt != nil {
		t.Fatalf("UpdateRunStatus() = %#v, want reviewing with nil finished_at", updated)
	}
}

func assertSQLiteWorkspaceRunUpsert(t *testing.T, ctx context.Context, store Store, wantID int64) {
	t.Helper()
	finishedAt := time.Unix(1_700_000_000, 0).UTC()
	upserted, err := store.UpsertRun(ctx, WorkspaceRun{
		RunKey:        "workspace-crud",
		DagKey:        "dag-crud",
		SourceRoot:    "C:/repo2",
		WorkspacePath: "C:/tmp/workspace-crud-2",
		Status:        "active",
		CreatedBy:     "other-creator",
		UpdatedBy:     "tester2",
		Metadata:      json.RawMessage(`{"phase":"upserted"}`),
		FinishedAt:    &finishedAt,
	})
	if err != nil {
		t.Fatalf("UpsertRun(update) error = %v", err)
	}
	if upserted.ID != wantID || upserted.CreatedBy != "tester" || upserted.SourceRoot != "C:/repo2" || upserted.FinishedAt == nil {
		t.Fatalf("UpsertRun(update) = %#v, want same id/creator, new source root, finished_at", upserted)
	}
}

func assertSQLiteWorkspaceFileCRUD(t *testing.T, ctx context.Context, store Store) {
	t.Helper()
	file, err := store.UpsertFile(ctx, WorkspaceRunFile{
		RunKey:             "workspace-crud",
		RelativePath:       "src/app.go",
		BaselineSHA256:     "base",
		WorkspaceSHA256:    "work",
		SourceSHA256Before: "before",
		SourceSHA256After:  "after",
		State:              "modified",
	})
	if err != nil {
		t.Fatalf("UpsertFile() error = %v", err)
	}
	if file.ID == 0 || file.State != "modified" {
		t.Fatalf("UpsertFile() = %#v, want modified with id", file)
	}
	gotFile, err := store.GetFile(ctx, "workspace-crud", "src/app.go")
	if err != nil {
		t.Fatalf("GetFile() error = %v", err)
	}
	if gotFile.WorkspaceSHA256 != "work" {
		t.Fatalf("GetFile().WorkspaceSHA256 = %q, want work", gotFile.WorkspaceSHA256)
	}
	files, err := store.ListFiles(ctx, ListFilesFilter{RunKey: "workspace-crud", State: "modified", Limit: 10})
	if err != nil {
		t.Fatalf("ListFiles() error = %v", err)
	}
	if len(files) != 1 || files[0].RelativePath != "src/app.go" {
		t.Fatalf("ListFiles() = %#v, want src/app.go", files)
	}
}

func TestSQLiteWorkspaceStatusCASRejectsStaleTransition(t *testing.T) {
	ctx := context.Background()
	db := openWorkspaceSQLiteDB(t)
	store := NewStore(db)

	if _, err := store.UpsertRun(ctx, WorkspaceRun{
		RunKey:        "workspace-run",
		DagKey:        "dag-1",
		SourceRoot:    "C:/repo",
		WorkspacePath: "C:/tmp/workspace-run",
		Status:        "active",
		CreatedBy:     "tester",
		UpdatedBy:     "tester",
		Metadata:      []byte(`{"stage":"start"}`),
	}); err != nil {
		t.Fatalf("UpsertRun() error = %v", err)
	}
	if _, err := store.TransitionRunStatus(ctx, TransitionRunStatusInput{
		RunKey:     "workspace-run",
		FromStatus: "active",
		Status:     "merged",
		UpdatedBy:  "tester",
		Metadata:   []byte(`{"stage":"merged"}`),
	}); err != nil {
		t.Fatalf("TransitionRunStatus(active->merged) error = %v", err)
	}
	_, err := store.TransitionRunStatus(ctx, TransitionRunStatusInput{
		RunKey:     "workspace-run",
		FromStatus: "active",
		Status:     "failed",
		UpdatedBy:  "tester",
		Metadata:   []byte(`{"stage":"stale"}`),
	})
	if !platformdb.IsNotFound(err) {
		t.Fatalf("stale TransitionRunStatus() error = %v, want not found/OCC miss", err)
	}
	run, err := store.GetRun(ctx, "workspace-run")
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if run.Status != "merged" {
		t.Fatalf("status after stale transition = %q, want merged", run.Status)
	}
}

func openWorkspaceSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	db, err := sql.Open("sqlite", workspaceSQLiteDSN(path))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(4)
	if err := sqliteruntime.RunMigrations(ctx, db, workspaceSQLiteMigrationsDir(t)); err != nil {
		t.Fatalf("run sqlite migrations: %v", err)
	}
	return db
}

func workspaceSQLiteDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout=5000")
	q.Add("_pragma", "foreign_keys=ON")
	q.Add("_pragma", "journal_mode=WAL")
	return path + "?" + q.Encode()
}

func workspaceSQLiteMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "internal", "platform", "db", "sqlite", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	return dir
}
