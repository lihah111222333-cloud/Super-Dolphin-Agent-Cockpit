package gopls

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindJSTSProjectRootWithinFindsFirstValidProject(t *testing.T) {
	root := t.TempDir()

	writeProjectMarker(t, filepath.Join(root, "node_modules", "ignored"), "package.json")
	want := filepath.Join(root, "apps", "web")
	writeProjectMarker(t, want, "tsconfig.json")

	got := findJSTSProjectRootWithin(root)
	if got != want {
		t.Fatalf("findJSTSProjectRootWithin(%q) = %q, want %q", root, got, want)
	}
}

func TestFindJSTSProjectRootWithinSkipsIgnoredDirectories(t *testing.T) {
	root := t.TempDir()

	writeProjectMarker(t, filepath.Join(root, "node_modules", "ignored"), "package.json")
	writeProjectMarker(t, filepath.Join(root, ".cache", "hidden"), "package.json")
	writeProjectMarker(t, filepath.Join(root, "dist", "site"), "jsconfig.json")

	got := findJSTSProjectRootWithin(root)
	if got != "" {
		t.Fatalf("findJSTSProjectRootWithin(%q) = %q, want empty result", root, got)
	}
}

func TestFindJSTSBootstrapFileWithinFindsFirstValidFile(t *testing.T) {
	root := t.TempDir()

	writeProjectMarker(t, filepath.Join(root, "node_modules", "ignored"), "index.ts")
	want := filepath.Join(root, "apps", "web", "main.ts")
	writeProjectMarker(t, filepath.Join(root, "apps", "web"), "main.ts")

	got := findJSTSBootstrapFileWithin(root)
	if got != want {
		t.Fatalf("findJSTSBootstrapFileWithin(%q) = %q, want %q", root, got, want)
	}
}

func TestFindJSTSBootstrapFileWithinSkipsIgnoredDirectories(t *testing.T) {
	root := t.TempDir()

	writeProjectMarker(t, filepath.Join(root, "node_modules", "ignored"), "index.ts")
	writeProjectMarker(t, filepath.Join(root, ".cache", "hidden"), "main.js")
	writeProjectMarker(t, filepath.Join(root, "dist", "site"), "app.tsx")

	got := findJSTSBootstrapFileWithin(root)
	if got != "" {
		t.Fatalf("findJSTSBootstrapFileWithin(%q) = %q, want empty result", root, got)
	}
}

func writeProjectMarker(t *testing.T, dir, marker string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}
	path := filepath.Join(dir, marker)
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
