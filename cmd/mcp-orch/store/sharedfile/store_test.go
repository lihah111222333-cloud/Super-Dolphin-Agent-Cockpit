package sharedfile

import (
	"context"
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
