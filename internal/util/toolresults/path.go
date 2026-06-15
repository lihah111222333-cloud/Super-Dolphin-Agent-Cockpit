// Package toolresults centralises the on-disk location of persisted tool-call
// results so writers (turn) and readers (memory nested ingest) stay locked
// to the same path without inverting the assembly dependency graph.
//
// memory.Module sits below turn in the platform dependency order
// (see internal/app/modules.go), so memory cannot import turn directly.
// Both modules depend on this package instead.
package toolresults

import (
	"os"
	"path/filepath"
	"strings"
)

// CacheDir returns the absolute path of the tool-results cache directory.
//
// Resolution: os.UserCacheDir() with os.TempDir() as fallback. Returns ""
// when both are unavailable; callers must treat the empty string as
// fail-closed (writers must error out, readers must skip without leaking
// the absence to the user).
//
// CacheDir does not create the directory. Writers should MkdirAll after
// retrieving the path; readers go through SafeReadEntrypoint, which
// fails closed when the directory is missing.
// CacheDir 处理缓存目录。
func CacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	if strings.TrimSpace(base) == "" {
		return ""
	}
	return filepath.Join(base, "super-agent-v3", "tool-results")
}
