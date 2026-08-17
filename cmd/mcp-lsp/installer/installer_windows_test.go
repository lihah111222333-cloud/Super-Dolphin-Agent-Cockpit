//go:build windows

package installer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecutableInDirPrefersWindowsExeSuffix(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "gopls.exe")
	if err := os.WriteFile(want, []byte("binary"), 0o644); err != nil {
		t.Fatalf("WriteFile(gopls.exe): %v", err)
	}
	got, ok := executableInDir(dir, "gopls")
	if !ok {
		t.Fatal("executableInDir() ok = false, want true")
	}
	if got != want {
		t.Fatalf("executableInDir() = %q, want %q", got, want)
	}
}
