package wails

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stagedClipboardFile creates a temp file with `clipboard-*.png` style name in
// dir and back-dates its mtime to ageBack ago. Returns its full path.
func stagedClipboardFile(t *testing.T, dir, base string, ageBack time.Duration) string {
	t.Helper()
	full := filepath.Join(dir, base)
	if err := os.WriteFile(full, []byte("\x89PNG"), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	if ageBack > 0 {
		when := time.Now().Add(-ageBack)
		if err := os.Chtimes(full, when, when); err != nil {
			t.Fatalf("chtimes %s: %v", full, err)
		}
	}
	return full
}

func TestCleanupRemovesStaleClipboardFiles(t *testing.T) {
	dir := t.TempDir()
	old := stagedClipboardFile(t, dir, "clipboard-old.png", 30*24*time.Hour)
	fresh := stagedClipboardFile(t, dir, "clipboard-fresh.png", 0)
	other := stagedClipboardFile(t, dir, "screenshot.png", 30*24*time.Hour)

	removed, kept := cleanupStaleClipboardImages(nil, dir, 7*24*time.Hour)
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if kept != 1 {
		t.Errorf("kept = %d, want 1 (the fresh one)", kept)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old file should be deleted: stat err=%v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh file should be retained: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("non-clipboard file should be untouched: %v", err)
	}
}

func TestCleanupRespectsZeroRetention(t *testing.T) {
	dir := t.TempDir()
	stagedClipboardFile(t, dir, "clipboard-old.png", 30*24*time.Hour)
	removed, kept := cleanupStaleClipboardImages(nil, dir, 0)
	if removed != 0 || kept != 0 {
		t.Errorf("retention=0 should be a no-op, got removed=%d kept=%d", removed, kept)
	}
}

func TestCleanupSkipsSubdirectories(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "clipboard-sub.png") // dir name matching pattern
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	removed, kept := cleanupStaleClipboardImages(nil, dir, 1*time.Hour)
	if removed != 0 || kept != 0 {
		t.Errorf("subdir should be skipped, got removed=%d kept=%d", removed, kept)
	}
	if _, err := os.Stat(subdir); err != nil {
		t.Errorf("subdir should remain: %v", err)
	}
}

func TestCleanupCandidateFilter(t *testing.T) {
	cases := map[string]bool{
		"clipboard-12345.png": true,
		"clipboard-abc.PNG":   true,
		"screenshot.png":      false,
		"clipboard-x.jpg":     false,
		"clipboard-":          false,
	}
	dir := t.TempDir()
	for name := range cases {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, entry := range entries {
		got[entry.Name()] = isClipboardCleanupCandidate(entry)
	}
	for name, want := range cases {
		if got[name] != want {
			t.Errorf("isClipboardCleanupCandidate(%q) = %v, want %v", name, got[name], want)
		}
	}
}

func TestCleanupNonExistentDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "ghost")
	removed, kept := cleanupStaleClipboardImages(nil, missing, 1*time.Hour)
	if removed != 0 || kept != 0 {
		t.Errorf("missing dir should yield zero, got removed=%d kept=%d", removed, kept)
	}
}
