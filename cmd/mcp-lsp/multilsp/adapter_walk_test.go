package multilsp

import (
	"path/filepath"
	"testing"
)

func TestFindProjectRootWithinReturnsWalkError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")

	got, err := findProjectRootWithin(root, []string{"package.json"}, nil)
	if err == nil {
		t.Fatalf("findProjectRootWithin(%q) error = nil, want walk error", root)
	}
	if got != "" {
		t.Fatalf("findProjectRootWithin(%q) = %q, want empty result on error", root, got)
	}
}

func TestFindBootstrapFileWithinReturnsWalkError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")

	got, err := findBootstrapFileWithin(root, []string{".ts"}, nil)
	if err == nil {
		t.Fatalf("findBootstrapFileWithin(%q) error = nil, want walk error", root)
	}
	if got != "" {
		t.Fatalf("findBootstrapFileWithin(%q) = %q, want empty result on error", root, got)
	}
}
