package sharedfile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	sharedfilefs "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/sharedfilefs"
)

func TestSharedfileFSConfigFromPackagedRuntimeUsesWritableHome(t *testing.T) {
	resources := filepath.Join(t.TempDir(), "Super Dolphin.app", "Contents", "Resources")
	appData := filepath.Join(t.TempDir(), "Library", "Application Support", "Super Dolphin")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	t.Setenv("SUPER_DOLPHIN_HOME", appData)

	got, err := sharedfileFSConfigFrom(&platformconfig.Config{ProjectRoot: resources})
	if err != nil {
		t.Fatalf("sharedfileFSConfigFrom() error = %v", err)
	}

	if got.CWD != appData {
		t.Fatalf("sharedfile CWD = %q, want writable app data root %q", got.CWD, appData)
	}
}

func TestPackagedSharedfileStoreWritesReportsUnderWritableHome(t *testing.T) {
	resources := filepath.Join(t.TempDir(), "Super Dolphin.app", "Contents", "Resources")
	appData := filepath.Join(t.TempDir(), "Library", "Application Support", "Super Dolphin")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	t.Setenv("SUPER_DOLPHIN_HOME", appData)

	cfg, err := sharedfileFSConfigFrom(&platformconfig.Config{ProjectRoot: resources})
	if err != nil {
		t.Fatalf("sharedfileFSConfigFrom() error = %v", err)
	}
	store := &store{q: newFakeRowStore().querier(), cfg: cfg}
	if _, err := store.Upsert(context.Background(), UpsertParams{
		Path:      "reports/daily_ai_video/script.md",
		Content:   "ok",
		UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	assertFileContent(t, filepath.Join(appData, sharedfilefs.SandboxDir, "reports/daily_ai_video/script.md"), "ok")
	assertFileMissing(t, filepath.Join(resources, sharedfilefs.SandboxDir, "reports/daily_ai_video/script.md"))
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s content = %q, want %q", path, got, want)
	}
}

func assertFileMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("%s exists, want missing", path)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", path, err)
	}
}
