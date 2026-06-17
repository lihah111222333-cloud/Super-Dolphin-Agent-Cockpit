package sharedfilefs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigEnabledAndThreshold(t *testing.T) {
	t.Parallel()

	if (Config{}).Enabled() {
		t.Fatal("zero config Enabled() = true, want false")
	}
	if !(Config{CWD: "/tmp/x"}).Enabled() {
		t.Fatal("Config{CWD:/tmp/x}.Enabled() = false, want true")
	}
	if got := (Config{}).ResolvedThreshold(); got != DefaultInlineThresholdBytes {
		t.Fatalf("zero ResolvedThreshold = %d, want %d", got, DefaultInlineThresholdBytes)
	}
	if got := (Config{InlineThresholdBytes: 256}).ResolvedThreshold(); got != 256 {
		t.Fatalf("ResolvedThreshold(256) = %d, want 256", got)
	}
}

func TestResolveAbsHappyPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := Config{CWD: dir}
	abs, err := cfg.ResolveAbs("handoff/task-1/notes.md")
	if err != nil {
		t.Fatalf("ResolveAbs error = %v", err)
	}
	want := filepath.Clean(filepath.Join(dir, SandboxDir, "handoff/task-1/notes.md"))
	if abs != want {
		t.Fatalf("abs = %q, want %q", abs, want)
	}
}

func TestResolveAbsRejectsSandboxEscape(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := Config{CWD: dir}
	// path.Clean of cleaned input would have caught traversal; ResolveAbs
	// is the second line of defense for callers who somehow bypass the
	// lexical validator (e.g., crafted absolute path that survives Clean).
	if _, err := cfg.ResolveAbs("../escape"); !errors.Is(err, ErrSandboxEscape) {
		t.Fatalf("ResolveAbs(../escape) err = %v, want ErrSandboxEscape", err)
	}
}

func TestResolveAbsDisabledWhenCWDEmpty(t *testing.T) {
	t.Parallel()

	if _, err := (Config{}).ResolveAbs("handoff/x.md"); !errors.Is(err, ErrDiskDisabled) {
		t.Fatalf("disabled ResolveAbs err = %v, want ErrDiskDisabled", err)
	}
}

func TestWriteAtomicHappyPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "handoff", "task-1", "notes.md")
	if err := WriteAtomic(target, []byte("hello world")); err != nil {
		t.Fatalf("WriteAtomic error = %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	if string(got) != "hello world" {
		t.Fatalf("content = %q, want %q", got, "hello world")
	}
	// No tmp leftover.
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatalf("ReadDir error = %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("leftover tmp file: %s", e.Name())
		}
	}
}

func TestWriteAtomicReplacesExisting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "handoff", "task-1", "notes.md")
	if err := WriteAtomic(target, []byte("v1")); err != nil {
		t.Fatalf("first write error = %v", err)
	}
	if err := WriteAtomic(target, []byte("v2-longer-content")); err != nil {
		t.Fatalf("second write error = %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "v2-longer-content" {
		t.Fatalf("content = %q, want v2-longer-content", got)
	}
}

func TestResolveReadAbsRejectsParentSymlinkEscape(t *testing.T) {
	t.Parallel()

	cfg, rel := configWithEscapingSharedfileLink(t)
	if _, err := cfg.ResolveReadAbs(rel); !errors.Is(err, ErrSandboxEscape) {
		t.Fatalf("ResolveReadAbs(parent symlink) err = %v, want ErrSandboxEscape", err)
	}
}

func TestResolveWriteAbsRejectsParentSymlinkEscape(t *testing.T) {
	t.Parallel()

	cfg, _ := configWithEscapingSharedfileLink(t)
	if _, err := cfg.ResolveWriteAbs("reports/new.md"); !errors.Is(err, ErrSandboxEscape) {
		t.Fatalf("ResolveWriteAbs(parent symlink) err = %v, want ErrSandboxEscape", err)
	}
}

func TestReadDiskMissingReturnsErrNotExist(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, _, err := ReadDisk(filepath.Join(dir, "missing"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadDisk(missing) err = %v, want fs.ErrNotExist", err)
	}
}

func configWithEscapingSharedfileLink(t *testing.T) (Config, string) {
	t.Helper()

	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	root := filepath.Join(dir, SandboxDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir sandbox root: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "reports")); err != nil {
		t.Skipf("symlink unavailable on this platform: %v", err)
	}
	return Config{CWD: dir}, "reports/secret.md"
}

func TestReadDiskRejectsDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir error = %v", err)
	}
	_, _, err := ReadDisk(subdir)
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("ReadDisk(dir) err = %v, want directory error", err)
	}
}

func TestRemoveDiskMissingIsSuccess(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := RemoveDisk(filepath.Join(dir, "never-existed")); err != nil {
		t.Fatalf("RemoveDisk(missing) err = %v, want nil", err)
	}
}

func TestRemoveDiskExistingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "x.md")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file err = %v", err)
	}
	if err := RemoveDisk(target); err != nil {
		t.Fatalf("RemoveDisk err = %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("file still exists, stat err = %v", err)
	}
}
