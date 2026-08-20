package toolchainidentity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDigestExecutableUsesExactOpenedBytes(t *testing.T) {
	t.Parallel()

	data := []byte("opened executable bytes\x00")
	got, err := DigestExecutable(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	wantSum := sha256.Sum256(data)
	want := "sha256:" + hex.EncodeToString(wantSum[:])
	if got != want {
		t.Fatalf("DigestExecutable() = %q, want %q", got, want)
	}
}

func TestDigestExecutableRejectsTypedNilReader(t *testing.T) {
	var file *os.File
	if _, err := DigestExecutable(file); err == nil {
		t.Fatal("DigestExecutable accepted a typed-nil reader")
	}
}

func TestDigestDirectoryCanonicalizesSortedRelativeFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for name, contents := range map[string]string{"z.txt": "z", "a/b.txt": "b", "a.txt": "a"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	first, err := DigestDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("DigestDirectory() = %q, want sha256 digest", first)
	}
	second, err := DigestDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("DigestDirectory() is not stable: %q != %q", first, second)
	}
}

func TestDigestDirectoryRejectsSymlinkAndNonRegularEntries(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	if _, err := DigestDirectory(root); err == nil {
		t.Fatal("DigestDirectory() accepted a symlink")
	}
	if _, err := DigestDirectory(target); err == nil {
		t.Fatal("DigestDirectory() accepted a regular file as a closure root")
	}
	if runtime.GOOS != "windows" {
		fifo := filepath.Join(root, "fifo")
		if _, err := exec.LookPath("mkfifo"); err == nil {
			if err := exec.Command("mkfifo", fifo).Run(); err != nil {
				t.Fatal(err)
			}
			if _, err := DigestDirectory(root); err == nil {
				t.Fatal("DigestDirectory() accepted a named pipe")
			}
		}
	}
}

func TestCompilerClosureDigestUsesExplicitCompilerAndBoundedEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell compiler fixture is POSIX-only")
	}
	root := t.TempDir()
	writeClosureFile(t, root, "src/z.go", "z")
	writeClosureFile(t, root, "src/a.go", "a")
	compiler := writeExecutable(t, filepath.Join(t.TempDir(), "compiler"), "#!/bin/sh\nif [ \"$1\" != env ] || [ \"$2\" != GOROOT ]; then exit 11; fi\nif [ \"$GOENV\" != off ] || [ \"$GOTOOLCHAIN\" != local ] || [ \"$GOPROXY\" != off ]; then exit 12; fi\nprintf '%s\\n' \"$TEST_TOOLCHAIN_ROOT\"\n")
	t.Setenv("TEST_TOOLCHAIN_ROOT", root)

	got, err := CompilerClosureDigest(context.Background(), compiler)
	if err != nil {
		t.Fatal(err)
	}
	otherRoot := t.TempDir()
	writeClosureFile(t, otherRoot, "src/a.go", "a")
	writeClosureFile(t, otherRoot, "src/z.go", "z")
	other, err := runCompilerClosure(t, compiler, otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got != other {
		t.Fatalf("closure digest includes absolute GOROOT: %q != %q", got, other)
	}

	writeClosureFile(t, root, "src/a.go", "changed")
	t.Setenv("TEST_TOOLCHAIN_ROOT", root)
	changed, err := CompilerClosureDigest(context.Background(), compiler)
	if err != nil {
		t.Fatal(err)
	}
	if changed == got {
		t.Fatal("closure digest did not change after a regular file changed")
	}
}

func TestDigestDirectoryLengthFramesAdversarialRelativePaths(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	writeClosureFile(t, first, "a\nb", "one")
	writeClosureFile(t, first, "a\\b", "two")
	writeClosureFile(t, second, "a\nb", "one")
	writeClosureFile(t, second, "a\\b", "changed")
	firstDigest, err := DigestDirectory(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := DigestDirectory(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == secondDigest {
		t.Fatal("length-framed manifest ignored an adversarial path/content change")
	}
}

func TestCompilerClosureDigestRejectsUnsafeCompilerPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable-mode fixture is unavailable")
	}
	root := t.TempDir()
	compiler := writeExecutable(t, filepath.Join(root, "compiler"), "#!/bin/sh\nprintf '%s\\n' \"$TEST_TOOLCHAIN_ROOT\"\n")
	t.Setenv("TEST_TOOLCHAIN_ROOT", root)
	notExecutable := filepath.Join(root, "not-executable")
	if err := os.WriteFile(notExecutable, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"relative":               filepath.Base(compiler),
		"unclean":                filepath.Dir(compiler) + string(filepath.Separator) + "." + string(filepath.Separator) + filepath.Base(compiler),
		"missing executable bit": notExecutable,
	} {
		if _, err := CompilerClosureDigest(context.Background(), path); err == nil {
			t.Fatalf("CompilerClosureDigest(%s) accepted %q", name, path)
		} else if FailureStage(err) != "compiler_path" {
			t.Fatalf("CompilerClosureDigest(%s) stage = %q", name, FailureStage(err))
		}
	}
	link := filepath.Join(root, "compiler-link")
	if err := os.Symlink(compiler, link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	if _, err := CompilerClosureDigest(context.Background(), link); err == nil {
		t.Fatal("CompilerClosureDigest accepted a compiler symlink")
	}
}

func TestCompilerClosureDigestDoesNotExposeAbsoluteGOROOT(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell compiler fixture is POSIX-only")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	compiler := writeExecutable(t, filepath.Join(t.TempDir(), "compiler"), "#!/bin/sh\nprintf '%s\\n' \"$TEST_TOOLCHAIN_ROOT\"\n")
	t.Setenv("TEST_TOOLCHAIN_ROOT", root)
	_, err := CompilerClosureDigest(context.Background(), compiler)
	if err == nil {
		t.Fatal("CompilerClosureDigest accepted a symlink in GOROOT")
	}
	if strings.Contains(err.Error(), root) {
		t.Fatalf("CompilerClosureDigest exposed absolute GOROOT in error: %v", err)
	}
	if FailureStage(err) != "closure_walk" {
		t.Fatalf("CompilerClosureDigest symlink stage = %q", FailureStage(err))
	}
}

func TestCompilerClosureDigestHonorsContextCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell compiler fixture is POSIX-only")
	}
	root := t.TempDir()
	compiler := writeExecutable(t, filepath.Join(root, "compiler"), "#!/bin/sh\nsleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := CompilerClosureDigest(ctx, compiler); err == nil {
		t.Fatal("CompilerClosureDigest accepted a cancelled compiler")
	} else if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("CompilerClosureDigest exceeded bounded cancellation: %s", elapsed)
	}
}

func runCompilerClosure(t *testing.T, compiler, root string) (string, error) {
	t.Helper()
	t.Setenv("TEST_TOOLCHAIN_ROOT", root)
	return CompilerClosureDigest(context.Background(), compiler)
}

func writeClosureFile(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeExecutable(t *testing.T, path, contents string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
