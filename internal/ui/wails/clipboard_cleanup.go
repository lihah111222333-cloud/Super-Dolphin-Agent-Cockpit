package wails

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// defaultClipboardRetention is the age beyond which temp clipboard PNGs
// (written by SaveClipboardImage) are removed at app startup. The bytes
// survive in claude CLI's session jsonl regardless, so dropping the temp
// file does not break history rendering — the frontend falls back to the
// data: URL recovered from history metadata.
const defaultClipboardRetention = 7 * 24 * time.Hour

// cleanupStaleClipboardImages walks dir and removes "clipboard-*.png" files
// whose modification time is older than retention. Errors on individual
// files are logged and skipped — cleanup is best-effort.
// cleanupStaleClipboardImages 处理cleanupstaleclipboardimages。
func cleanupStaleClipboardImages(logger *slog.Logger, dir string, retention time.Duration) (removed, kept int) {
	if retention <= 0 || strings.TrimSpace(dir) == "" {
		return 0, 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		logCleanupWarn(logger, "read tmp dir failed", "dir", dir, "err", err)
		return 0, 0
	}
	cutoff := time.Now().Add(-retention)
	for _, entry := range entries {
		switch handleClipboardEntry(logger, dir, entry, cutoff) {
		case cleanupResultRemoved:
			removed++
		case cleanupResultKept:
			kept++
		}
	}
	logCleanupSummary(logger, dir, retention, removed, kept)
	return removed, kept
}

type cleanupResult int

const (
	cleanupResultSkipped cleanupResult = iota
	cleanupResultRemoved
	cleanupResultKept
)

// handleClipboardEntry classifies one directory entry and, when appropriate,
// removes it. Splitting this out keeps cleanupStaleClipboardImages itself
// inside the project's cyclomatic-complexity budget.
func handleClipboardEntry(logger *slog.Logger, dir string, entry fs.DirEntry, cutoff time.Time) cleanupResult {
	if !isClipboardCleanupCandidate(entry) {
		return cleanupResultSkipped
	}
	info, err := entry.Info()
	if err != nil {
		return cleanupResultSkipped
	}
	if info.ModTime().After(cutoff) {
		return cleanupResultKept
	}
	full := filepath.Join(dir, entry.Name())
	if err := os.Remove(full); err != nil {
		logCleanupWarn(logger, "remove failed", "path", full, "err", err)
		return cleanupResultSkipped
	}
	return cleanupResultRemoved
}

func logCleanupSummary(logger *slog.Logger, dir string, retention time.Duration, removed, kept int) {
	if logger == nil || (removed == 0 && kept == 0) {
		return
	}
	logger.Info("clipboard cleanup",
		"dir", dir,
		"removed", removed,
		"kept", kept,
		"retention", retention.String(),
	)
}

func isClipboardCleanupCandidate(entry fs.DirEntry) bool {
	if entry == nil || entry.IsDir() {
		return false
	}
	name := entry.Name()
	if !strings.HasPrefix(name, "clipboard-") {
		return false
	}
	return strings.EqualFold(filepath.Ext(name), ".png")
}

func logCleanupWarn(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Warn("clipboard cleanup: "+msg, args...)
}
