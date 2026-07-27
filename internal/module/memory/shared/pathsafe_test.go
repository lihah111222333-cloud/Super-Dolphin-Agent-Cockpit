package memshared_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	memory "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory"
	shared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/shared"
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

// SafeReadEntrypoint 的直接用例固定入口文件读取的错误分类。
// 这些测试绕过上层 memory 包，确保 helper 自身在缺失、越界、目录和权限场景下保持 sentinel 错误。

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

func TestSafeReadEntrypointLimitRejectsOversizedFileWithoutTruncation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	index := filepath.Join(root, "tool-output.txt")
	if err := os.WriteFile(index, []byte("12345"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	raw, _, err := shared.SafeReadEntrypointLimit(root, index, 4)
	if !errors.Is(err, shared.ErrSafeReadTooLarge) {
		t.Fatalf("SafeReadEntrypointLimit() error = %v, want ErrSafeReadTooLarge", err)
	}
	if raw != nil {
		t.Fatalf("SafeReadEntrypointLimit() raw = %q, want nil on overflow", raw)
	}
}

func TestSafeReadEntrypointLimitAcceptsExactLimit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	index := filepath.Join(root, "tool-output.txt")
	if err := os.WriteFile(index, []byte("1234"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	raw, _, err := shared.SafeReadEntrypointLimit(root, index, 4)
	if err != nil {
		t.Fatalf("SafeReadEntrypointLimit() error = %v, want nil", err)
	}
	if string(raw) != "1234" {
		t.Fatalf("SafeReadEntrypointLimit() raw = %q, want %q", raw, "1234")
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
	// 悬空软链解析到 os.ErrNotExist 时按 NotFound 处理；对调用方来说它等价于目标文件缺失。
	if !errors.Is(err, shared.ErrSafeReadNotFound) {
		t.Fatalf("err = %v, want ErrSafeReadNotFound for dangling symlink", err)
	}
}

func TestSafeReadEntrypointAcceptsSymlinkedRoot(t *testing.T) {
	// root 本身是软链时要先解析真实路径再做包含性比较，避免合法入口文件被误判为越界。
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
	// 父目录软链指向 root 外部时必须被包含性检查拒绝。
	// 这里覆盖 indexPath 本身不是软链、但其父目录需要解析的分支。
	jail := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "MEMORY.md"), []byte("exfil"), 0o644); err != nil {
		t.Fatalf("seed outside: %v", err)
	}
	parentLink := filepath.Join(jail, "sub")
	if err := os.Symlink(outside, parentLink); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	// indexPath 本身不是软链；解析父目录后候选路径会落到 outside。
	_, _, err := shared.SafeReadEntrypoint(jail, filepath.Join(parentLink, "MEMORY.md"))
	if !errors.Is(err, shared.ErrSafeReadContainment) {
		t.Fatalf("err = %v, want ErrSafeReadContainment", err)
	}
}

func TestSafeReadEntrypointBrokenLinkSentinelWrapsCause(t *testing.T) {
	// EvalSymlinks 因非 ENOENT 原因失败时，helper 应返回 ErrSafeReadBrokenLink 并保留底层错误。
	// 软链环在 POSIX 上可稳定触发这类错误。
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
	// 错误链必须同时暴露 sentinel 和底层 EvalSymlinks 原因，便于调用方用 errors.Is 精确分类。
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

// TestSafeReadEntrypointBrokenLinkPermissionUnwrapsCause 验证软链目标解析遇到 EACCES 时，
// 返回错误既能命中 ErrSafeReadBrokenLink，也能通过 errors.Is 命中 os.ErrPermission。
func TestSafeReadEntrypointBrokenLinkPermissionUnwrapsCause(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses unix directory mode permission checks")
	}
	root := t.TempDir()
	// 软链仍指向 root 内部，但目标父目录不可读；包含性检查通过后 EvalSymlinks 会因权限失败。
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
	// 移除父目录执行位，让 EvalSymlinks(linkPath) 在解析目标组件时返回 EACCES。
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
