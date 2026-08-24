package remotecicli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitObjectBlobBatchRejectsMissingAndNonBlobResponses(t *testing.T) {
	root, treeSHA := newGitObjectBlobBatchRepository(t, map[string][]byte{
		"nested/file.txt": []byte("content\n"),
	})

	for _, testCase := range []struct {
		name string
		path string
	}{
		{name: "missing", path: "missing.txt"},
		{name: "tree", path: "nested"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			batch := openGitObjectBlobBatch(t, context.Background(), root, treeSHA)
			if _, err := batch.ReadBlob(context.Background(), treeSHA, testCase.path); err == nil || !strings.Contains(err.Error(), "response is not a blob") {
				t.Fatalf("ReadBlob(%q) error = %v, want non-blob response", testCase.path, err)
			}
			assertGitObjectBlobBatchReclaimed(t, batch)
			assertGitObjectBlobBatchCloseIdempotent(t, batch)
		})
	}
}

func TestGitObjectBlobBatchRejectsOversizedBlobAndReclaimsProcess(t *testing.T) {
	root, treeSHA := newGitObjectBlobBatchRepository(t, map[string][]byte{
		"oversized.bin": bytes.Repeat([]byte{'x'}, gitObjectOutputMaxBytes+1),
	})
	batch := openGitObjectBlobBatch(t, context.Background(), root, treeSHA)

	if _, err := batch.ReadBlob(context.Background(), treeSHA, "oversized.bin"); err == nil || !strings.Contains(err.Error(), "response exceeds limit") {
		t.Fatalf("ReadBlob(oversized.bin) error = %v, want bounded fail-fast", err)
	}
	assertGitObjectBlobBatchReclaimed(t, batch)
	assertGitObjectBlobBatchCloseIdempotent(t, batch)
}

func TestGitObjectBlobBatchContextCancellationReclaimsProcess(t *testing.T) {
	root, treeSHA := newGitObjectBlobBatchRepository(t, map[string][]byte{
		"file.txt": []byte("content\n"),
	})
	ctx, cancel := context.WithCancel(context.Background())
	batch := openGitObjectBlobBatch(t, ctx, root, treeSHA)
	cancel()

	if _, err := batch.ReadBlob(ctx, treeSHA, "file.txt"); err == nil || !strings.Contains(err.Error(), "batch is unavailable") {
		t.Fatalf("ReadBlob(canceled context) error = %v, want unavailable", err)
	}
	if err := batch.Close(); err == nil || !strings.Contains(err.Error(), "batch close failed") {
		t.Fatalf("Close() after context cancellation error = %v, want process cancellation evidence", err)
	}
	assertGitObjectBlobBatchReclaimed(t, batch)
	assertGitObjectBlobBatchCloseIdempotent(t, batch)
}

func TestGitObjectBlobBatchCloseRejectsRepositoryStorageIdentityDrift(t *testing.T) {
	root, treeSHA := newGitObjectBlobBatchRepository(t, map[string][]byte{
		"file.txt": []byte("content\n"),
	})
	batch := openGitObjectBlobBatch(t, context.Background(), root, treeSHA)
	if body, err := batch.ReadBlob(context.Background(), treeSHA, "file.txt"); err != nil || string(body) != "content\n" {
		t.Fatalf("ReadBlob(file.txt) = %q, %v", body, err)
	}

	gitPath := filepath.Join(root, ".git")
	if err := os.Rename(gitPath, filepath.Join(root, ".git-original")); err != nil {
		t.Fatalf("replace Git storage: %v", err)
	}
	if err := os.Mkdir(gitPath, 0o700); err != nil {
		t.Fatalf("create drifted Git storage: %v", err)
	}

	if err := batch.Close(); err == nil || !strings.Contains(err.Error(), "repository root identity changed") {
		t.Fatalf("Close() after Git storage drift error = %v, want identity rejection", err)
	}
	assertGitObjectBlobBatchReclaimed(t, batch)
	assertGitObjectBlobBatchCloseIdempotent(t, batch)
}

func newGitObjectBlobBatchRepository(t *testing.T, files map[string][]byte) (string, string) {
	t.Helper()
	root := t.TempDir()
	runGitFixture(t, root, "init", "--quiet")
	runGitFixture(t, root, "config", "user.email", "batch@example.invalid")
	runGitFixture(t, root, "config", "user.name", "Batch Test")
	for relativePath, data := range files {
		path := filepath.Join(root, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("create fixture directory: %v", err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write fixture %s: %v", relativePath, err)
		}
	}
	runGitFixture(t, root, "add", "-A")
	runGitFixture(t, root, "commit", "--quiet", "-m", "batch fixture")
	treeSHA := runGitFixture(t, root, "rev-parse", "HEAD^{tree}")
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve fixture root: %v", err)
	}
	return resolvedRoot, treeSHA
}

func openGitObjectBlobBatch(t *testing.T, ctx context.Context, root, treeSHA string) *gitObjectBlobBatch {
	t.Helper()
	opened, err := (gitObjectResolver{repositoryRoot: root}).newListedBlobBatch(ctx, treeSHA)
	if err != nil {
		t.Fatalf("newListedBlobBatch() error = %v", err)
	}
	batch, ok := opened.(*gitObjectBlobBatch)
	if !ok {
		t.Fatalf("newListedBlobBatch() type = %T, want *gitObjectBlobBatch", opened)
	}
	t.Cleanup(func() {
		if !batch.closed {
			_ = batch.Close()
		}
	})
	return batch
}

func assertGitObjectBlobBatchReclaimed(t *testing.T, batch *gitObjectBlobBatch) {
	t.Helper()
	if !batch.closed || batch.command == nil || batch.command.ProcessState == nil {
		t.Fatalf("batch process was not reclaimed: closed=%v command=%#v", batch.closed, batch.command)
	}
}

func assertGitObjectBlobBatchCloseIdempotent(t *testing.T, batch *gitObjectBlobBatch) {
	t.Helper()
	if err := batch.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if err := batch.Close(); err != nil {
		t.Fatalf("third Close() error = %v", err)
	}
	if !errors.Is(batch.command.Process.Signal(os.Interrupt), os.ErrProcessDone) {
		t.Fatal("reclaimed Git batch process still accepts signals")
	}
}
