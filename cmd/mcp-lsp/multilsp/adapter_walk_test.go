package multilsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFindProjectRootWithinReturnsWalkError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")

	got, err := findProjectRootWithin(context.Background(), root, []string{"package.json"}, nil)
	if err == nil {
		t.Fatalf("findProjectRootWithin(%q) error = nil, want walk error", root)
	}
	if got != "" {
		t.Fatalf("findProjectRootWithin(%q) = %q, want empty result on error", root, got)
	}
}

func TestFindBootstrapFileWithinReturnsWalkError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")

	got, err := findBootstrapFileWithin(context.Background(), root, []string{".ts"}, nil)
	if err == nil {
		t.Fatalf("findBootstrapFileWithin(%q) error = nil, want walk error", root)
	}
	if got != "" {
		t.Fatalf("findBootstrapFileWithin(%q) = %q, want empty result on error", root, got)
	}
}

func TestFindProjectRootWithinHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := findProjectRootWithin(ctx, t.TempDir(), []string{"package.json"}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("findProjectRootWithin() error = %v, want context.Canceled", err)
	}
	if got != "" {
		t.Fatalf("findProjectRootWithin() = %q, want empty result", got)
	}
}

func TestBoundedProjectWalkRejectsDepthLimit(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "one", "two")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	err := boundedProjectWalk(context.Background(), root, 1, 100, func(string, os.DirEntry, error) error { return nil })
	if !errors.Is(err, errProjectWalkDepthLimit) {
		t.Fatalf("boundedProjectWalk() error = %v, want errProjectWalkDepthLimit", err)
	}
}

func TestBoundedProjectWalkRejectsEntryLimit(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	err := boundedProjectWalk(context.Background(), root, 10, 2, func(string, os.DirEntry, error) error { return nil })
	if !errors.Is(err, errProjectWalkEntryLimit) {
		t.Fatalf("boundedProjectWalk() error = %v, want errProjectWalkEntryLimit", err)
	}
}
