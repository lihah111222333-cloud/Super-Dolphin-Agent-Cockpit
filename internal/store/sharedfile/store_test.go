package sharedfile

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	sharedfilefs "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilefs"
	sharedfilegitignore "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilegitignore"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	_ "modernc.org/sqlite"
)

type sharedFileQuerierStub struct {
	getFn    func(context.Context, sqlc.GetSharedFileParams) (sqlc.SharedFile, error)
	listFn   func(context.Context, sqlc.ListSharedFilesParams) ([]sqlc.SharedFile, error)
	deleteFn func(context.Context, sqlc.DeleteSharedFileParams) (int64, error)
	upsertFn func(context.Context, sqlc.UpsertSharedFileParams) (sqlc.SharedFile, error)
}

func (s *sharedFileQuerierStub) GetSharedFile(ctx context.Context, arg sqlc.GetSharedFileParams) (sqlc.SharedFile, error) {
	if s.getFn != nil {
		return s.getFn(ctx, arg)
	}
	return sqlc.SharedFile{}, nil
}

func (s *sharedFileQuerierStub) ListSharedFiles(ctx context.Context, arg sqlc.ListSharedFilesParams) ([]sqlc.SharedFile, error) {
	if s.listFn != nil {
		return s.listFn(ctx, arg)
	}
	return nil, nil
}

func (s *sharedFileQuerierStub) DeleteSharedFile(ctx context.Context, arg sqlc.DeleteSharedFileParams) (int64, error) {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, arg)
	}
	return 0, nil
}

func (s *sharedFileQuerierStub) UpsertSharedFile(ctx context.Context, arg sqlc.UpsertSharedFileParams) (sqlc.SharedFile, error) {
	if s.upsertFn != nil {
		return s.upsertFn(ctx, arg)
	}
	return sqlc.SharedFile{}, nil
}

func TestGetMapsRow(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_000_000, 0).UTC()
	var captured string
	s := &store{q: &sharedFileQuerierStub{
		getFn: func(_ context.Context, arg sqlc.GetSharedFileParams) (sqlc.SharedFile, error) {
			captured = arg.Path
			return sqlc.SharedFile{
				Path:      "dag/dag-1/readme.md",
				Content:   "hello",
				UpdatedBy: "alice",
				CreatedAt: now.UnixMilli(),
				UpdatedAt: now.UnixMilli(),
			}, nil
		},
	}}
	got, err := s.Get(context.Background(), "dag/dag-1/readme.md")
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if captured != "dag/dag-1/readme.md" {
		t.Fatalf("Get() forwarded path = %q", captured)
	}
	if got == nil || got.Path != "dag/dag-1/readme.md" || got.Content != "hello" || got.UpdatedBy != "alice" {
		t.Fatalf("Get() row mapped incorrectly: %+v", got)
	}
}

func TestGetWrapsError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("not found")
	s := &store{q: &sharedFileQuerierStub{
		getFn: func(context.Context, sqlc.GetSharedFileParams) (sqlc.SharedFile, error) {
			return sqlc.SharedFile{}, sentinel
		},
	}}
	_, err := s.Get(context.Background(), "any")
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("Get() err = %v, want wrap of sentinel", err)
	}
}

func TestListForwardsPrefixAndLimit(t *testing.T) {
	t.Parallel()
	now := time.Unix(2_000_000, 0).UTC()
	var captured sqlc.ListSharedFilesParams
	s := &store{q: &sharedFileQuerierStub{
		listFn: func(_ context.Context, arg sqlc.ListSharedFilesParams) ([]sqlc.SharedFile, error) {
			captured = arg
			return []sqlc.SharedFile{
				{Path: "dag/dag-1/x.md", Content: "a", UpdatedBy: "u", CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()},
				{Path: "dag/dag-1/y.md", Content: "b", UpdatedBy: "u", CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()},
			}, nil
		},
	}}
	got, err := s.List(context.Background(), ListFilter{Prefix: "dag/dag-1/", Limit: 20})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if captured.Prefix != "dag/dag-1/" || captured.LimitCount != 20 {
		t.Fatalf("List() forwarded wrong params: %+v", captured)
	}
	if len(got) != 2 {
		t.Fatalf("List() len = %d, want 2", len(got))
	}
	if got[0].Path != "dag/dag-1/x.md" || got[1].Path != "dag/dag-1/y.md" {
		t.Fatalf("List() rows mapped out of order: %+v", got)
	}
}

func TestListPrefixMatchesRealSQLiteQuery(t *testing.T) {
	t.Parallel()

	db := openSharedFileSQLite(t)
	execSharedFileSQL(t, db, `CREATE TABLE shared_files (
		path TEXT PRIMARY KEY,
		content TEXT NOT NULL,
		content_location TEXT NOT NULL DEFAULT 'inline' CHECK (content_location IN ('inline', 'disk')),
		updated_by TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);`)
	now := time.Unix(2_000_000, 0).UTC().UnixMilli()
	execSharedFileSQL(t, db, `INSERT INTO shared_files (path, content, updated_by, created_at, updated_at) VALUES
		('dag/dag-1/readme.md', 'a', 'u', ?, ?),
		('handoff/other.md', 'b', 'u', ?, ?);`, now, now, now-1, now-1)

	s := &store{q: sqlc.New(db)}
	got, err := s.List(context.Background(), ListFilter{Prefix: "dag/dag-1", Limit: 10})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Path != "dag/dag-1/readme.md" {
		t.Fatalf("List() prefix result = %+v, want only dag/dag-1/readme.md", got)
	}
}

func TestListReturnsEmptySliceWhenNoRows(t *testing.T) {
	t.Parallel()
	s := &store{q: &sharedFileQuerierStub{}}
	got, err := s.List(context.Background(), ListFilter{})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("List() got = %v, want non-nil empty slice", got)
	}
}

func TestUpsertForwardsPayloadAndMapsRow(t *testing.T) {
	t.Parallel()
	now := time.Unix(3_000_000, 0).UTC()
	var captured sqlc.UpsertSharedFileParams
	var emitted uidto.UISharedFilesChanged
	s := &store{q: &sharedFileQuerierStub{
		upsertFn: func(_ context.Context, arg sqlc.UpsertSharedFileParams) (sqlc.SharedFile, error) {
			captured = arg
			return sqlc.SharedFile{
				Path:            arg.Path,
				Content:         arg.Content,
				ContentLocation: arg.ContentLocation,
				UpdatedBy:       arg.UpdatedBy,
				CreatedAt:       now.UnixMilli(),
				UpdatedAt:       now.UnixMilli(),
			}, nil
		},
	}, emitSharedFilesChanged: func(ev uidto.UISharedFilesChanged) { emitted = ev }}
	got, err := s.Upsert(context.Background(), UpsertParams{
		Path:      "reports/demo.md",
		Content:   "hello",
		UpdatedBy: "system",
	})
	if err != nil {
		t.Fatalf("Upsert() unexpected error: %v", err)
	}
	if captured.Path != "reports/demo.md" || captured.Content != "hello" || captured.ContentLocation != contentLocationInline || captured.UpdatedBy != "system" {
		t.Fatalf("Upsert() forwarded wrong params: %+v", captured)
	}
	if got == nil || got.Path != captured.Path || got.Content != "hello" || got.UpdatedBy != "system" {
		t.Fatalf("Upsert() row mapped incorrectly: %+v", got)
	}
	assertSharedFilesChangedEvent(t, emitted, "reports/demo.md", "write")
}

func TestUpsertWritesContentLocationToRealSQLite(t *testing.T) {
	t.Parallel()

	db := openSharedFileSQLite(t)
	execSharedFileSQL(t, db, `CREATE TABLE shared_files (
		path TEXT PRIMARY KEY,
		content TEXT NOT NULL,
		content_location TEXT NOT NULL DEFAULT 'inline' CHECK (content_location IN ('inline', 'disk')),
		updated_by TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);`)
	s := &store{q: sqlc.New(db)}

	_, err := s.Upsert(context.Background(), UpsertParams{
		Path:      "reports/demo.md",
		Content:   "hello",
		UpdatedBy: "system",
	})
	if err != nil {
		t.Fatalf("Upsert() unexpected error: %v", err)
	}
	var got string
	if err := db.QueryRow(`SELECT content_location FROM shared_files WHERE path = ?`, "reports/demo.md").Scan(&got); err != nil {
		t.Fatalf("query content_location: %v", err)
	}
	if got != contentLocationInline {
		t.Fatalf("content_location = %q, want %q", got, contentLocationInline)
	}
}

func TestUpsertDiskBackedFailsBeforeWriteWhenGitignoreEnsureFails(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, ".gitignore"), 0o755); err != nil {
		t.Fatalf("mkdir .gitignore sentinel: %v", err)
	}
	sharedfilegitignore.ResetForTests()
	t.Cleanup(sharedfilegitignore.ResetForTests)
	upsertCalled := false
	s := &store{
		q: &sharedFileQuerierStub{
			getFn: func(context.Context, sqlc.GetSharedFileParams) (sqlc.SharedFile, error) {
				return sqlc.SharedFile{}, sql.ErrNoRows
			},
			upsertFn: func(context.Context, sqlc.UpsertSharedFileParams) (sqlc.SharedFile, error) {
				upsertCalled = true
				return sqlc.SharedFile{}, nil
			},
		},
		cfg: sharedfilefs.Config{CWD: cwd, InlineThresholdBytes: 1},
	}

	_, err := s.Upsert(context.Background(), UpsertParams{
		Path:      "reports/gitignore-fail.md",
		Content:   "body",
		UpdatedBy: "tester",
	})
	if err == nil || !strings.Contains(err.Error(), "sharedfilegitignore") {
		t.Fatalf("Upsert() error = %v, want gitignore ensure failure", err)
	}
	if upsertCalled {
		t.Fatal("UpsertSharedFile called after gitignore ensure failure")
	}
	if _, statErr := os.Stat(filepath.Join(cwd, ".agnet", "shared", "reports", "gitignore-fail.md")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("disk file stat error = %v, want not exist", statErr)
	}
}

func TestUpsertWrapsQuerierError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("db unavailable")
	s := &store{q: &sharedFileQuerierStub{
		upsertFn: func(context.Context, sqlc.UpsertSharedFileParams) (sqlc.SharedFile, error) {
			return sqlc.SharedFile{}, sentinel
		},
	}}
	_, err := s.Upsert(context.Background(), UpsertParams{Path: "dag/dag-1/notes.md"})
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("Upsert() err = %v, want wrap of sentinel", err)
	}
}

func TestListWrapsQuerierError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("db timeout")
	s := &store{q: &sharedFileQuerierStub{
		listFn: func(context.Context, sqlc.ListSharedFilesParams) ([]sqlc.SharedFile, error) {
			return nil, sentinel
		},
	}}
	_, err := s.List(context.Background(), ListFilter{})
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("List() err = %v, want wrap of sentinel", err)
	}
}

func TestDeleteForwardsPathAndReturnsCount(t *testing.T) {
	t.Parallel()
	var captured string
	var emitted uidto.UISharedFilesChanged
	s := &store{q: &sharedFileQuerierStub{
		deleteFn: func(_ context.Context, arg sqlc.DeleteSharedFileParams) (int64, error) {
			captured = arg.Path
			return 1, nil
		},
	}, emitSharedFilesChanged: func(ev uidto.UISharedFilesChanged) { emitted = ev }}
	count, err := s.Delete(context.Background(), "dag/dag-1/readme.md")
	if err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
	if captured != "dag/dag-1/readme.md" {
		t.Fatalf("Delete() forwarded path = %q", captured)
	}
	if count != 1 {
		t.Fatalf("Delete() count = %d, want 1", count)
	}
	assertSharedFilesChangedEvent(t, emitted, "dag/dag-1/readme.md", "delete")
}

func TestDeleteWrapsQuerierError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("db gone")
	s := &store{q: &sharedFileQuerierStub{
		deleteFn: func(context.Context, sqlc.DeleteSharedFileParams) (int64, error) {
			return 0, sentinel
		},
	}}
	_, err := s.Delete(context.Background(), "x")
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("Delete() err = %v, want wrap of sentinel", err)
	}
}

func assertSharedFilesChangedEvent(t *testing.T, got uidto.UISharedFilesChanged, wantPath, wantAction string) {
	t.Helper()
	if got.Path != wantPath {
		t.Fatalf("changed event path = %q, want %q; event=%+v", got.Path, wantPath, got)
	}
	if got.Action != wantAction {
		t.Fatalf("changed event action = %q, want %q; event=%+v", got.Action, wantAction, got)
	}
	if got.Timestamp.IsZero() {
		t.Fatalf("changed event timestamp is zero; event=%+v", got)
	}
}

func openSharedFileSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	})
	return db
}

func execSharedFileSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec sql: %v\n%s", err, query)
	}
}
