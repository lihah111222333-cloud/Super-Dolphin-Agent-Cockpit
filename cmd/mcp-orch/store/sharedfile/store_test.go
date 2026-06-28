package sharedfile

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	sharedfilefs "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilefs"
)

func TestSharedFileDiskOnlyMissingFileFails(t *testing.T) {
	t.Parallel()

	store := newStoreWithConfig(sqlc.New(newFakeImportDB(t)), sharedfilefs.Config{
		CWD:                  t.TempDir(),
		InlineThresholdBytes: 1,
	})
	const path = "reports/run-1/result.md"
	if _, err := store.Upsert(context.Background(), UpsertParams{
		Path:      path,
		Content:   "disk-only content",
		UpdatedBy: "tester",
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if err := os.Remove(filepath.Join(store.cfg.SandboxRoot(), path)); err != nil {
		t.Fatalf("remove disk body: %v", err)
	}

	got, err := store.Get(context.Background(), path)
	if err == nil {
		t.Fatalf("Get() = %#v, nil error; want missing disk body failure", got)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Get() error = %v, want fs.ErrNotExist", err)
	}
	if !strings.Contains(err.Error(), "disk content") {
		t.Fatalf("Get() error = %v, want disk content context", err)
	}
}

func TestSharedFileEmptyInlineMissingDiskFallsBackToInline(t *testing.T) {
	t.Parallel()

	store := newStoreWithConfig(sqlc.New(newFakeImportDB(t)), sharedfilefs.Config{
		CWD:                  t.TempDir(),
		InlineThresholdBytes: 1,
	})
	const path = "reports/run-1/empty.md"
	if _, err := store.Upsert(context.Background(), UpsertParams{
		Path:      path,
		Content:   "",
		UpdatedBy: "tester",
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if err := os.Remove(filepath.Join(store.cfg.SandboxRoot(), path)); err != nil {
		t.Fatalf("remove disk body: %v", err)
	}

	got, err := store.Get(context.Background(), path)
	if err != nil {
		t.Fatalf("Get() empty inline error = %v, want inline fallback", err)
	}
	if got.Content != "" {
		t.Fatalf("Content = %q, want empty inline content", got.Content)
	}
}

func TestSharedFilePersistsContentLocation(t *testing.T) {
	t.Parallel()

	db := newFakeImportDB(t)
	store := newStoreWithConfig(sqlc.New(db), sharedfilefs.Config{
		CWD:                  t.TempDir(),
		InlineThresholdBytes: 1,
	})
	tests := []struct {
		name    string
		path    string
		content string
		want    string
	}{
		{name: "inline empty", path: "reports/run-1/empty.md", content: "", want: "inline"},
		{name: "disk large", path: "reports/run-1/large.md", content: "large", want: "disk"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := store.Upsert(context.Background(), UpsertParams{
				Path:      tt.path,
				Content:   tt.content,
				UpdatedBy: "tester",
			}); err != nil {
				t.Fatalf("Upsert() error = %v", err)
			}
			var got string
			if err := db.QueryRowContext(context.Background(), `SELECT content_location FROM shared_files WHERE path = ?`, tt.path).Scan(&got); err != nil {
				t.Fatalf("query content_location: %v", err)
			}
			if got != tt.want {
				t.Fatalf("content_location = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSharedFileUpsertDoesNotOverwriteDiskOnDBFailure(t *testing.T) {
	t.Parallel()

	db := newFakeImportDB(t)
	store := newStoreWithConfig(sqlc.New(db), sharedfilefs.Config{
		CWD:                  t.TempDir(),
		InlineThresholdBytes: 1,
	})
	const path = "reports/run-1/db-failure.md"
	if _, err := store.Upsert(context.Background(), UpsertParams{
		Path:      path,
		Content:   "old",
		UpdatedBy: "seed",
	}); err != nil {
		t.Fatalf("seed Upsert() error = %v", err)
	}
	abs := filepath.Join(store.cfg.SandboxRoot(), path)
	if err := db.Close(); err != nil {
		t.Fatalf("close db before failing Upsert: %v", err)
	}

	_, err := store.Upsert(context.Background(), UpsertParams{
		Path:      path,
		Content:   "new",
		UpdatedBy: "agent",
	})
	if err == nil {
		t.Fatal("Upsert() error = nil, want DB failure")
	}
	got, readErr := os.ReadFile(abs)
	if readErr != nil {
		t.Fatalf("read disk body after failed DB upsert: %v", readErr)
	}
	if string(got) != "old" {
		t.Fatalf("disk body after failed DB upsert = %q, want old", string(got))
	}
}

func TestSharedFileUpsertRollsBackDBOnPublishFailure(t *testing.T) {
	db := newFakeImportDB(t)
	cfg := sharedfilefs.Config{CWD: t.TempDir(), InlineThresholdBytes: 1}
	seedStore := newStoreWithConfig(sqlc.New(db), cfg)
	const path = "reports/run-1/publish-failure.md"
	if _, err := seedStore.Upsert(context.Background(), UpsertParams{
		Path:      path,
		Content:   "old",
		UpdatedBy: "seed",
	}); err != nil {
		t.Fatalf("seed Upsert() error = %v", err)
	}
	abs := filepath.Join(seedStore.cfg.SandboxRoot(), path)
	dir := filepath.Dir(abs)
	hooked := makeReadOnlyAfterAgentUpsert(t, db, path, dir)
	store := newStoreWithConfig(sqlc.New(hooked), cfg)
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	_, err := store.Upsert(context.Background(), UpsertParams{
		Path:      path,
		Content:   "new",
		UpdatedBy: "agent",
	})
	if err == nil {
		t.Fatal("Upsert() error = nil, want publish failure")
	}
	if restoreErr := os.Chmod(dir, 0o755); restoreErr != nil {
		t.Fatalf("restore dir permissions: %v", restoreErr)
	}
	assertSharedFileMetadata(t, db, path, "", "seed")
	got, readErr := os.ReadFile(abs)
	if readErr != nil {
		t.Fatalf("read disk body after failed publish: %v", readErr)
	}
	if string(got) != "old" {
		t.Fatalf("disk body after failed publish = %q, want old", string(got))
	}
}

func TestSharedFileDeleteDiskFailureKeepsDBIndex(t *testing.T) {
	db := newFakeImportDB(t)
	store := newStoreWithConfig(sqlc.New(db), sharedfilefs.Config{
		CWD:                  t.TempDir(),
		InlineThresholdBytes: 1,
	})
	const path = "reports/run-1/delete-failure.md"
	if _, err := store.Upsert(context.Background(), UpsertParams{
		Path:      path,
		Content:   "body",
		UpdatedBy: "seed",
	}); err != nil {
		t.Fatalf("seed Upsert() error = %v", err)
	}
	abs := filepath.Join(store.cfg.SandboxRoot(), path)
	dir := filepath.Dir(abs)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	_, err := store.Delete(context.Background(), path)
	if err == nil {
		t.Fatal("Delete() error = nil, want disk failure")
	}
	if restoreErr := os.Chmod(dir, 0o755); restoreErr != nil {
		t.Fatalf("restore dir permissions: %v", restoreErr)
	}
	assertSharedFileMetadata(t, db, path, "", "seed")
	got, readErr := os.ReadFile(abs)
	if readErr != nil {
		t.Fatalf("read disk body after failed delete: %v", readErr)
	}
	if string(got) != "body" {
		t.Fatalf("disk body after failed delete = %q, want body", string(got))
	}
}

func makeReadOnlyAfterAgentUpsert(t *testing.T, db *fakeImportDB, path, dir string) *queryRowHookDB {
	t.Helper()
	return &queryRowHookDB{
		DB: db.DB,
		afterQueryRow: func(query string, args ...any) {
			if isAgentUpsertQuery(query, args, path) {
				if err := os.Chmod(dir, 0o500); err != nil {
					t.Fatalf("chmod dir read-only: %v", err)
				}
			}
		},
	}
}

func isAgentUpsertQuery(query string, args []any, path string) bool {
	return strings.Contains(query, "INSERT INTO shared_files") &&
		len(args) >= 4 &&
		args[0] == path &&
		args[3] == "agent"
}

type queryRowHookDB struct {
	*sql.DB
	afterQueryRow func(query string, args ...any)
}

func (db *queryRowHookDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	row := db.DB.QueryRowContext(ctx, query, args...)
	if db.afterQueryRow != nil {
		db.afterQueryRow(query, args...)
	}
	return row
}
