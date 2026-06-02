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

func TestShouldExcludePathCoversObservedWorkspaceNoise(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		{name: "linux temp workspace root", path: "/tmp/workspace/src/main.go", want: false},
		{name: "workspace tmp dir", path: "/tmp/workspace/tmp/cache.txt", want: true},
		{name: "rust target dir", path: "codex-rs/target/debug/build.rs", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldExcludePath(tc.path); got != tc.want {
				t.Fatalf("shouldExcludePath(%q) = %t, want %t", tc.path, got, tc.want)
			}
		})
	}
}
