package sharedfile

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sharedfilefs "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/sharedfilefs"
)

func TestSharedFileListPrefixIsPrefixOnly(t *testing.T) {
	t.Parallel()
	db := newFakeImportDB(t)
	store := NewStore(db.DB)
	seedSharedFileRows(t, db,
		"reports/alpha.md",
		"reports/nested/beta.md",
		"archive/reports/alpha.md",
		"reports_extra.md",
	)

	got, err := store.List(context.Background(), ListFilter{Prefix: "reports/", Limit: 20})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	paths := sharedFilePaths(got)
	if strings.Join(paths, ",") != "reports/nested/beta.md,reports/alpha.md" {
		t.Fatalf("List(prefix reports/) paths = %#v, want only reports/ prefix", paths)
	}
}

func TestSharedFileListPrefixEscapesLikeWildcards(t *testing.T) {
	t.Parallel()
	db := newFakeImportDB(t)
	store := NewStore(db.DB)
	seedSharedFileRows(t, db,
		"reports/%literal.md",
		"reports/_literal.md",
		"reports/aliteral.md",
	)

	got, err := store.List(context.Background(), ListFilter{Prefix: "reports/%", Limit: 20})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	paths := sharedFilePaths(got)
	if strings.Join(paths, ",") != "reports/%literal.md" {
		t.Fatalf("List(prefix reports/%%) paths = %#v, want literal percent prefix only", paths)
	}
}

func seedSharedFileRows(t *testing.T, db *fakeImportDB, paths ...string) {
	t.Helper()
	for i, path := range paths {
		if _, err := db.ExecContext(context.Background(), `INSERT INTO shared_files (path, content, content_location, updated_by, created_at, updated_at) VALUES (?, '', 'inline', 'tester', ?, ?)`, path, i+1, i+1); err != nil {
			t.Fatalf("insert shared file %q: %v", path, err)
		}
	}
}

func sharedFilePaths(files []SharedFile) []string {
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = file.Path
	}
	return paths
}

func TestSharedFileDiskOnlyMissingFileFails(t *testing.T) {
	t.Parallel()

	store := newStoreWithConfig(newFakeImportDB(t).DB, sharedfilefs.Config{
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

	store := newStoreWithConfig(newFakeImportDB(t).DB, sharedfilefs.Config{
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
	store := newStoreWithConfig(db.DB, sharedfilefs.Config{
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

func TestSharedFileUpsertFailsBeforeWriteWhenGitignoreEnsureFails(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, ".gitignore"), 0o755); err != nil {
		t.Fatalf("mkdir .gitignore sentinel: %v", err)
	}
	db := newFakeImportDB(t)
	store := newStoreWithConfig(db.DB, sharedfilefs.Config{
		CWD:                  cwd,
		InlineThresholdBytes: 1,
	})

	_, err := store.Upsert(context.Background(), UpsertParams{
		Path:      "reports/gitignore-fail.md",
		Content:   "body",
		UpdatedBy: "tester",
	})
	if err == nil || !strings.Contains(err.Error(), "sharedfilegitignore") {
		t.Fatalf("Upsert() error = %v, want gitignore ensure failure", err)
	}
	if _, statErr := os.Stat(filepath.Join(cwd, ".agnet", "shared", "reports", "gitignore-fail.md")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("disk file stat error = %v, want not exist", statErr)
	}
	var count int
	if scanErr := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM shared_files WHERE path = ?`, "reports/gitignore-fail.md").Scan(&count); scanErr != nil {
		t.Fatalf("query shared_files: %v", scanErr)
	}
	if count != 0 {
		t.Fatalf("shared_files rows = %d, want 0 after gitignore failure", count)
	}
}

func TestSharedFileUpsertDoesNotOverwriteDiskOnDBFailure(t *testing.T) {
	t.Parallel()

	db := newFakeImportDB(t)
	store := newStoreWithConfig(db.DB, sharedfilefs.Config{
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
	seedStore := newStoreWithConfig(db.DB, cfg)
	const path = "reports/run-1/publish-failure.md"
	if _, err := seedStore.Upsert(context.Background(), UpsertParams{
		Path:      path,
		Content:   "old",
		UpdatedBy: "seed",
	}); err != nil {
		t.Fatalf("seed Upsert() error = %v", err)
	}
	abs := filepath.Join(seedStore.cfg.SandboxRoot(), path)
	if err := os.Remove(abs); err != nil {
		t.Fatalf("remove seeded body: %v", err)
	}
	if err := os.Mkdir(abs, 0o755); err != nil {
		t.Fatalf("replace body with directory: %v", err)
	}
	store := newStoreWithConfig(db.DB, cfg)

	_, err := store.Upsert(context.Background(), UpsertParams{
		Path:      path,
		Content:   "new",
		UpdatedBy: "agent",
	})
	if err == nil {
		t.Fatal("Upsert() error = nil, want publish failure")
	}
	assertSharedFileMetadata(t, db, path, "", "seed")
	info, statErr := os.Stat(abs)
	if statErr != nil || !info.IsDir() {
		t.Fatalf("publish failure target = %#v, error = %v, want directory unchanged", info, statErr)
	}
}

func TestSharedFileDeleteDiskFailureKeepsDBIndex(t *testing.T) {
	db := newFakeImportDB(t)
	store := newStoreWithConfig(db.DB, sharedfilefs.Config{
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
