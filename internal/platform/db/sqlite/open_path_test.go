package sqlite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenRejectsPathWithSurroundingWhitespace 确保 SQLite 打开不会静默改写调用方路径。
func TestOpenRejectsPathWithSurroundingWhitespace(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, " authority.db ")

	if _, err := Open(t.Context(), OpenOptions{Path: path}); err == nil || !strings.Contains(err.Error(), "surrounding whitespace") {
		t.Fatalf("Open() error = %v, want surrounding-whitespace rejection", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("whitespace path stat error = %v, want file not created", err)
	}
}
