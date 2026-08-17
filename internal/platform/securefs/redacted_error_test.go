package securefs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestWrapErrorForPathPreservesChainAndRedactsPath 锁定配置、SQLite、runtime cache 和 installer wrapper 的边界契约。
func TestWrapErrorForPathPreservesChainAndRedactsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-state.db")
	original := &os.PathError{Op: "open", Path: path, Err: syscall.Errno(5)}
	wrapped := WrapErrorForPath(original, path)

	var pathErr *os.PathError
	if !errors.As(wrapped, &pathErr) || pathErr != original {
		t.Fatalf("wrapped error lost *os.PathError chain: %v", wrapped)
	}
	if got := wrapped.Error(); got == "" || containsSecurefsPathToken(got, path, filepath.Dir(path)) {
		t.Fatalf("wrapped error leaked path: %q", got)
	}
	if !strings.Contains(wrapped.Error(), "open") {
		t.Fatalf("wrapped error = %q, want safe operation context", wrapped)
	}

	typed := NewWindowsPermissionError("ACL check", path, original)
	typedWrapped := WrapErrorForPath(typed, path)
	var permissionErr *WindowsPermissionError
	if !errors.As(typedWrapped, &permissionErr) || permissionErr == nil {
		t.Fatalf("typed Windows permission chain was not preserved: %v", typedWrapped)
	}
}

func containsSecurefsPathToken(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if candidate != "" && strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
