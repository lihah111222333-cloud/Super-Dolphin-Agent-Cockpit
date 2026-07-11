package multilsp

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestPersistentCacheConfiguredPathFailureReturnsError(t *testing.T) {
	cfg := lspCacheConfig{Persistent: true, Path: filepath.Join(t.TempDir(), "missing", "cache.json")}
	blocker := filepath.Dir(cfg.Path)
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	_, err := newLSPCacheStore(cfg)
	if err == nil || !strings.Contains(err.Error(), "persistent cache") {
		t.Fatalf("newLSPCacheStore() error = %v, want persistent cache failure", err)
	}
}

func TestPersistentCacheWriteFailureReturnsError(t *testing.T) {
	dir := t.TempDir()
	store, err := newLSPCacheStore(lspCacheConfig{Persistent: true, Dir: dir})
	if err != nil {
		t.Fatalf("newLSPCacheStore() error = %v", err)
	}
	makeCacheDirUnwritableForTest(t, dir)

	err = store.Upsert(lspCacheValue{Key: lspCacheKey{URI: "file:///repo/a.go"}})
	if err == nil || !strings.Contains(err.Error(), "persistent cache") {
		t.Fatalf("Upsert() error = %v, want persistent write failure", err)
	}
	if !store.persistent || !store.persistentReady {
		t.Fatalf("store disabled persistence after write failure; persistent=%v ready=%v", store.persistent, store.persistentReady)
	}
}

func TestPersistentCacheLoadFailureReturnsError(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, lspCacheFileName)
	if err := os.WriteFile(cachePath, []byte("{bad-json"), 0o644); err != nil {
		t.Fatalf("write corrupt cache: %v", err)
	}

	_, err := newLSPCacheStore(lspCacheConfig{Persistent: true, Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "persistent cache") || !strings.Contains(err.Error(), "load") {
		t.Fatalf("newLSPCacheStore() error = %v, want persistent cache load failure", err)
	}
}

func TestBootstrapCoordinatorForReturnsPersistentCacheLoadError(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, lspCacheFileName)
	if err := os.WriteFile(cachePath, []byte("{bad-json"), 0o644); err != nil {
		t.Fatalf("write corrupt cache: %v", err)
	}
	t.Setenv(lspCachePersistentEnv, "1")
	t.Setenv(lspCacheDirEnv, dir)
	mgr := &manager{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	_, err := bootstrapCoordinatorFor(mgr)
	if err == nil || !strings.Contains(err.Error(), "persistent cache") || !strings.Contains(err.Error(), "load") {
		t.Fatalf("bootstrapCoordinatorFor() error = %v, want persistent cache load failure", err)
	}
}

func TestBootstrapCoordinatorForReturnsPersistentCacheSetupError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "cache")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	t.Setenv(lspCachePersistentEnv, "1")
	t.Setenv(lspCacheDirEnv, blocker)
	mgr := &manager{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	_, err := bootstrapCoordinatorFor(mgr)
	if err == nil || !strings.Contains(err.Error(), "persistent cache") {
		t.Fatalf("bootstrapCoordinatorFor() error = %v, want persistent cache setup failure", err)
	}
}

func TestBootstrapDocumentPropagatesPersistentCacheWriteError(t *testing.T) {
	root := t.TempDir()
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"cache-write"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "const value = 1\n")
	cacheDir := t.TempDir()
	t.Setenv(lspCachePersistentEnv, "1")
	t.Setenv(lspCacheDirEnv, cacheDir)
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: &p2DiagnosticsFactory{}}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	if _, err := bootstrapCoordinatorFor(mgr); err != nil {
		t.Fatalf("bootstrapCoordinatorFor() setup error = %v", err)
	}
	makeCacheDirUnwritableForTest(t, cacheDir)

	err := mgr.BootstrapDocument(common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}}), fileURIFromPath(target))
	if err == nil || !strings.Contains(err.Error(), "persistent cache") {
		t.Fatalf("BootstrapDocument() error = %v, want persistent cache write failure", err)
	}
}
