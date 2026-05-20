package search

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsSearchCandidatePropagatesInfoError(t *testing.T) {
	entry := fakeDirEntry{
		name:    "a.go",
		infoErr: errors.New("info boom"),
	}

	_, err := isSearchCandidate("/repo/a.go", entry, 1024)
	if err == nil || !strings.Contains(err.Error(), "info boom") {
		t.Fatalf("isSearchCandidate() error = %v, want info boom", err)
	}
}

func TestIsBinaryFilePropagatesOpenError(t *testing.T) {
	_, err := isBinaryFile(filepath.Join(t.TempDir(), "missing.go"))
	if err == nil || !strings.Contains(err.Error(), "open") {
		t.Fatalf("isBinaryFile() error = %v, want open failure", err)
	}
}
