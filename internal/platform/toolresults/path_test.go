package toolresults

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestCacheDirNonEmptyOnTypicalHost asserts the happy path: a real host
// always provides at least UserCacheDir or TempDir, so CacheDir must
// return a non-empty absolute path under super-agent-v3/tool-results.
func TestCacheDirNonEmptyOnTypicalHost(t *testing.T) {
	got := CacheDir()
	if got == "" {
		t.Fatal("CacheDir() = \"\"; want non-empty path on a normal host")
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("CacheDir() = %q; want absolute path", got)
	}
	wantSuffix := filepath.Join("super-agent-v3", "tool-results")
	if !strings.HasSuffix(got, wantSuffix) {
		t.Fatalf("CacheDir() = %q; want suffix %q", got, wantSuffix)
	}
}
