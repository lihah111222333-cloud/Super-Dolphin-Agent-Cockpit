package lspplatform

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestCanonicalDirectoryPathAcceptsTempDirectory 验证普通临时目录可以被规范化。
func TestCanonicalDirectoryPathAcceptsTempDirectory(t *testing.T) {
	directory := t.TempDir()

	got, err := CanonicalDirectoryPath(filepath.Join(directory, "."))
	if err != nil {
		t.Fatalf("CanonicalDirectoryPath() error = %v", err)
	}
	assertCanonicalSameFile(t, directory, got)
}

// TestCanonicalExistingPathAcceptsTempFile 验证现存普通文件可以被规范化。
func TestCanonicalExistingPathAcceptsTempFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "fixture.txt")
	if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write temporary file: %v", err)
	}

	got, err := CanonicalExistingPath(file)
	if err != nil {
		t.Fatalf("CanonicalExistingPath() error = %v", err)
	}
	assertCanonicalSameFile(t, file, got)
}

// TestCanonicalDirectoryPathRejectsFile 验证目录专用入口不会接受普通文件。
func TestCanonicalDirectoryPathRejectsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write temporary file: %v", err)
	}

	got, err := CanonicalDirectoryPath(file)
	if err == nil {
		t.Fatalf("CanonicalDirectoryPath() returned %q for a file, want an error", got)
	}
	if got != "" {
		t.Fatalf("CanonicalDirectoryPath() returned path %q with error %v", got, err)
	}
}

// TestCanonicalPathMissingPreservesNotExist 验证路径不存在时保留可分类的错误链。
func TestCanonicalPathMissingPreservesNotExist(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	resolvers := []struct {
		name    string
		resolve func(string) (string, error)
	}{
		{name: "directory", resolve: CanonicalDirectoryPath},
		{name: "existing", resolve: CanonicalExistingPath},
	}

	for _, resolver := range resolvers {
		t.Run(resolver.name, func(t *testing.T) {
			got, err := resolver.resolve(missing)
			if err == nil {
				t.Fatalf("resolver returned %q for a missing path", got)
			}
			if got != "" {
				t.Fatalf("resolver returned path %q with error %v", got, err)
			}
			if !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("error = %v, want fs.ErrNotExist in the error chain", err)
			}
		})
	}
}

func assertCanonicalSameFile(t *testing.T, want, got string) {
	t.Helper()
	if got == "" {
		t.Fatal("canonical path is empty")
	}
	wantInfo, err := os.Stat(want)
	if err != nil {
		t.Fatalf("stat expected path: %v", err)
	}
	gotInfo, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat canonical path %q: %v", got, err)
	}
	if !os.SameFile(wantInfo, gotInfo) {
		t.Fatalf("canonical path %q does not identify %q", got, want)
	}
}
