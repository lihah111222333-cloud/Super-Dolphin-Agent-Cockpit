package memshared_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	memory "github.com/anthropic-ai/super-agent-v3/internal/module/memory"
	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
)

func TestValidateMemoryRootRejectsRelativePath(t *testing.T) {
	if _, err := shared.ValidateMemoryRoot("relative/path"); !errors.Is(err, shared.ErrInvalidMemoryRoot) {
		t.Fatalf("ValidateMemoryRoot(relative/path) error = %v, want %v", err, shared.ErrInvalidMemoryRoot)
	}
}

func TestValidateMemoryRootNormalizesTrailingSeparator(t *testing.T) {
	root := filepath.Join(newTestMemoryRoot(t), "nested")
	validated, err := shared.ValidateMemoryRoot(root + string(os.PathSeparator))
	if err != nil {
		t.Fatalf("ValidateMemoryRoot() error = %v", err)
	}
	want := filepath.Clean(root) + string(os.PathSeparator)
	if validated != want {
		t.Fatalf("ValidateMemoryRoot() = %q, want %q", validated, want)
	}
}

func TestValidateMemoryRootRejectsUNCLikeDoubleSlash(t *testing.T) {
	if _, err := shared.ValidateMemoryRoot("//server/share"); !errors.Is(err, shared.ErrInvalidMemoryRoot) {
		t.Fatalf("ValidateMemoryRoot(//server/share) error = %v, want %v", err, shared.ErrInvalidMemoryRoot)
	}
}

func TestValidateMemoryWritePathRejectsTraversal(t *testing.T) {
	root := filepath.Join(newTestMemoryRoot(t), "allowed")
	if _, err := memory.ValidateMemoryWritePath(root, filepath.Join("..", "escape.md")); !errors.Is(err, memory.ErrInvalidMemoryWritePath) {
		t.Fatalf("ValidateMemoryWritePath() error = %v, want %v", err, memory.ErrInvalidMemoryWritePath)
	}
}

func TestValidateMemoryWritePathRejectsDanglingSymlink(t *testing.T) {
	root := filepath.Join(newTestMemoryRoot(t), "allowed")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", root, err)
	}
	linkPath := filepath.Join(root, "bad")
	if err := os.Symlink(filepath.Join(root, "missing-target"), linkPath); err != nil {
		t.Skipf("Symlink unsupported: %v", err)
	}
	if _, err := memory.ValidateMemoryWritePath(root, filepath.Join("bad", "entry.md")); !errors.Is(err, memory.ErrInvalidMemoryWritePath) {
		t.Fatalf("ValidateMemoryWritePath(dangling symlink) error = %v, want %v", err, memory.ErrInvalidMemoryWritePath)
	}
}

func TestValidateMemoryReadPathRejectsTraversal(t *testing.T) {
	root := filepath.Join(newTestMemoryRoot(t), "allowed")
	if _, err := memory.ValidateMemoryReadPath(root, filepath.Join("..", "escape.md")); !errors.Is(err, memory.ErrInvalidMemoryReadPath) {
		t.Fatalf("ValidateMemoryReadPath() error = %v, want %v", err, memory.ErrInvalidMemoryReadPath)
	}
}

func TestValidateMemoryReadPathRejectsSymlinkEscape(t *testing.T) {
	root := filepath.Join(newTestMemoryRoot(t), "allowed")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", root, err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", outside, err)
	}
	linkPath := filepath.Join(root, "escaped.md")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("Symlink unsupported: %v", err)
	}
	if _, err := memory.ValidateMemoryReadPath(root, linkPath); !errors.Is(err, memory.ErrInvalidMemoryReadPath) {
		t.Fatalf("ValidateMemoryReadPath(symlink escape) error = %v, want %v", err, memory.ErrInvalidMemoryReadPath)
	}
}

func newTestMemoryRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "memory-root")
}

// Phase 2.1.AB.1: direct coverage of the canonical SafeReadEntrypoint
// failure modes. Existing tests in the main memory package exercise
// SafeReadEntrypoint indirectly via the (now-removed) wrapper; these
// pin the sentinel-error contract at the helper level so future
// refactors must preserve it.

func TestSafeReadEntrypointReadsInRootFile(t *testing.T) {
	root := t.TempDir()
	index := filepath.Join(root, "MEMORY.md")
	if err := os.WriteFile(index, []byte("- entry\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	raw, info, err := shared.SafeReadEntrypoint(root, index)
	if err != nil {
		t.Fatalf("SafeReadEntrypoint() err = %v, want nil", err)
	}
	if string(raw) != "- entry\n" {
		t.Fatalf("content = %q, want %q", raw, "- entry\n")
	}
	if info == nil || info.IsDir() {
		t.Fatalf("info = %+v, want regular file", info)
	}
}

func TestSafeReadEntrypointReturnsNotFoundForMissingFile(t *testing.T) {
	root := t.TempDir()
	index := filepath.Join(root, "missing.md")
	_, _, err := shared.SafeReadEntrypoint(root, index)
	if !errors.Is(err, shared.ErrSafeReadNotFound) {
		t.Fatalf("err = %v, want ErrSafeReadNotFound", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want errors.Is os.ErrNotExist (so legacy callers keep working)", err)
	}
}

func TestSafeReadEntrypointReturnsNotFoundForMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	_, _, err := shared.SafeReadEntrypoint(root, filepath.Join(root, "MEMORY.md"))
	if !errors.Is(err, shared.ErrSafeReadNotFound) {
		t.Fatalf("err = %v, want ErrSafeReadNotFound", err)
	}
}

func TestSafeReadEntrypointReturnsContainmentForSymlinkEscape(t *testing.T) {
	jail := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("exfil"), 0o644); err != nil {
		t.Fatalf("seed outside: %v", err)
	}
	link := filepath.Join(jail, "MEMORY.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	_, _, err := shared.SafeReadEntrypoint(jail, link)
	if !errors.Is(err, shared.ErrSafeReadContainment) {
		t.Fatalf("err = %v, want ErrSafeReadContainment", err)
	}
}

func TestSafeReadEntrypointReturnsIsDirForDirectoryTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "MEMORY.md")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, _, err := shared.SafeReadEntrypoint(root, target)
	if !errors.Is(err, shared.ErrSafeReadIsDir) {
		t.Fatalf("err = %v, want ErrSafeReadIsDir", err)
	}
}

func TestSafeReadEntrypointReturnsBrokenLinkForDanglingSymlink(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "MEMORY.md")
	if err := os.Symlink(filepath.Join(root, "does-not-exist"), link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	_, _, err := shared.SafeReadEntrypoint(root, link)
	// Dangling symlink: EvalSymlinks fails with os.ErrNotExist, which we
	// classify as NotFound (not BrokenLink) since it is operationally
	// indistinguishable from a missing file at the symlink target. This
	// pins that classification.
	if !errors.Is(err, shared.ErrSafeReadNotFound) {
		t.Fatalf("err = %v, want ErrSafeReadNotFound for dangling symlink", err)
	}
}

func TestSafeReadEntrypointAcceptsSymlinkedRoot(t *testing.T) {
	// The root itself is supplied as a symlink: SafeReadEntrypoint must
	// EvalSymlinks the root before doing the containment compare so the
	// resolved candidate stays inside the resolved root. Without this
	// the in-root file would be falsely rejected as out-of-scope.
	realRoot := t.TempDir()
	index := filepath.Join(realRoot, "MEMORY.md")
	if err := os.WriteFile(index, []byte("- entry\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rootLink := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(realRoot, rootLink); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	raw, _, err := shared.SafeReadEntrypoint(rootLink, filepath.Join(rootLink, "MEMORY.md"))
	if err != nil {
		t.Fatalf("SafeReadEntrypoint(symlinked root) err = %v, want nil", err)
	}
	if string(raw) != "- entry\n" {
		t.Fatalf("content = %q, want %q", raw, "- entry\n")
	}
}

func TestSafeReadEntrypointRejectsParentDirSymlinkEscape(t *testing.T) {
	// A parent directory that is a symlink pointing OUTSIDE the
	// nominal root must be rejected by containment. This pins the
	// non-symlink branch of SafeReadEntrypoint where the helper
	// resolves filepath.Dir(indexPath) and re-joins Base(indexPath).
	jail := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "MEMORY.md"), []byte("exfil"), 0o644); err != nil {
		t.Fatalf("seed outside: %v", err)
	}
	parentLink := filepath.Join(jail, "sub")
	if err := os.Symlink(outside, parentLink); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	// indexPath itself is NOT a symlink (it's a regular file under the
	// symlinked parent), so the helper takes the EvalSymlinks(Dir)
	// branch and the resolved candidate ends up under `outside`.
	_, _, err := shared.SafeReadEntrypoint(jail, filepath.Join(parentLink, "MEMORY.md"))
	if !errors.Is(err, shared.ErrSafeReadContainment) {
		t.Fatalf("err = %v, want ErrSafeReadContainment", err)
	}
}

func TestSafeReadEntrypointBrokenLinkSentinelWrapsCause(t *testing.T) {
	// Phase 2.1.AB.2: when EvalSymlinks fails with a non-ENOENT cause,
	// the helper returns ErrSafeReadBrokenLink AND keeps the underlying
	// cause in the errors chain via fmt.Errorf("%w: ...: %w", ...).
	// A symlink loop reliably triggers a non-ENOENT EvalSymlinks error
	// (typically ELOOP / "too many links") on POSIX.
	root := t.TempDir()
	loopA := filepath.Join(root, "a")
	loopB := filepath.Join(root, "b")
	if err := os.Symlink(loopB, loopA); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := os.Symlink(loopA, loopB); err != nil {
		t.Fatalf("seed loop second leg: %v", err)
	}
	_, _, err := shared.SafeReadEntrypoint(root, loopA)
	if !errors.Is(err, shared.ErrSafeReadBrokenLink) {
		t.Fatalf("err = %v, want ErrSafeReadBrokenLink for symlink loop", err)
	}
	// Confirm the chain has multi-wrap structure (two %w verbs in
	// fmt.Errorf) so the underlying EvalSymlinks cause is reachable
	// alongside the sentinel. Single-wrap would not satisfy the
	// multi-Unwrap interface.
	multi, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("err type = %T, want multi-wrap (Unwrap() []error). err = %v", err, err)
	}
	if len(multi.Unwrap()) < 2 {
		t.Fatalf("multi.Unwrap() len = %d, want >=2 (sentinel + cause)", len(multi.Unwrap()))
	}
}

func TestSafeReadEntrypointReturnsPermissionForUnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		testSafeReadEntrypointReturnsPermissionForUnreadableFileWindows(t)
		return
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses unix file mode permission checks")
	}
	root := t.TempDir()
	index := filepath.Join(root, "MEMORY.md")
	if err := os.WriteFile(index, []byte("data"), 0o000); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(index, 0o644) })
	_, _, err := shared.SafeReadEntrypoint(root, index)
	if err == nil {
		t.Fatalf("err = nil, want a permission error")
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("err = %v, want errors.Is os.ErrPermission", err)
	}
}

func testSafeReadEntrypointReturnsPermissionForUnreadableFileWindows(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	index := filepath.Join(root, "MEMORY.md")
	if err := os.WriteFile(index, []byte("data"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	user := os.Getenv("USERDOMAIN") + `\` + os.Getenv("USERNAME")
	if strings.Trim(user, `\`) == "" {
		t.Skip("Windows user identity unavailable for ACL read-deny test")
	}
	if out, err := exec.Command("icacls", index, "/deny", user+":(R)").CombinedOutput(); err != nil {
		t.Fatalf("icacls deny read error = %v output=%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = exec.Command("icacls", index, "/remove:d", user).CombinedOutput()
	})
	_, _, err := shared.SafeReadEntrypoint(root, index)
	if err == nil {
		t.Fatalf("err = nil, want a permission error")
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("err = %v, want errors.Is os.ErrPermission", err)
	}
}

// TestSafeReadEntrypointBrokenLinkPermissionUnwrapsCause verifies that when
// EvalSymlinks fails with EACCES (not ENOENT) while resolving a symlink
// target, the returned error chain still satisfies
// errors.Is(err, os.ErrPermission) through the double-%w wrap in
// safeReadBrokenLinkOrNotFound. Regression guard: the previous coverage
// (TestSafeReadEntrypointBrokenLinkSentinelWrapsCause) only exercised the
// symlink-loop variant (ELOOP), leaving permission propagation through the
// broken-link wrap silently dependent on the OS error implementing Is.
func TestSafeReadEntrypointBrokenLinkPermissionUnwrapsCause(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses unix directory mode permission checks")
	}
	root := t.TempDir()
	// Stage a real file behind an unreadable directory; the symlink leg
	// stays inside root so containment passes, but EvalSymlinks must
	// traverse the locked directory and fail with EACCES.
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	actual := filepath.Join(locked, "MEMORY.md")
	if err := os.WriteFile(actual, []byte("data"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	linkPath := filepath.Join(root, "MEMORY.md")
	if err := os.Symlink(actual, linkPath); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	// Strip exec bit on the parent so EvalSymlinks(linkPath) returns
	// EACCES while trying to resolve the target component.
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("lock dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	_, _, err := shared.SafeReadEntrypoint(root, linkPath)
	if err == nil {
		t.Fatalf("err = nil, want a permission-bearing broken-link error")
	}
	if !errors.Is(err, shared.ErrSafeReadBrokenLink) {
		t.Fatalf("err = %v, want errors.Is ErrSafeReadBrokenLink", err)
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("err = %v, want errors.Is os.ErrPermission through double-%%w wrap", err)
	}
}
