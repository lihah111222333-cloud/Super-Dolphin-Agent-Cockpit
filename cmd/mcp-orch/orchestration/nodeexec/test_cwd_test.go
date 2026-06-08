package nodeexec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testCWD(t *testing.T, name string) string {
	t.Helper()
	base := filepath.Join(os.TempDir(), "super-dolphin-test-cwd", sanitizeTestCWDPart(t.Name()))
	path := filepath.Join(base, sanitizeTestCWDPart(name))
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("create test cwd %s: %v", path, err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(base)
	})
	return filepath.ToSlash(path)
}

func sanitizeTestCWDPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "empty"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '_'
		}
	}, value)
}
