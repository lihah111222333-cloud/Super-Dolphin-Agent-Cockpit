package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestContainsPathIncludesEqualAndChildren(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "pkg", "file.go")

	if !ContainsPath(root, root) {
		t.Fatalf("ContainsPath(%q, %q) = false, want true", root, root)
	}
	if !ContainsPath(root, child) {
		t.Fatalf("ContainsPath(%q, %q) = false, want true", root, child)
	}
}

func TestContainsPathRejectsSiblingPrefix(t *testing.T) {
	root := t.TempDir()
	sibling := root + "-sibling"

	if ContainsPath(root, sibling) {
		t.Fatalf("ContainsPath(%q, %q) = true, want false", root, sibling)
	}
}

func TestContainsPathNormalizesMissingChildUnderSymlinkedRoot(t *testing.T) {
	realRoot := t.TempDir()
	linkRoot := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	target := filepath.Join(linkRoot, "missing", "child.txt")

	if !ContainsPath(linkRoot, target) {
		t.Fatalf("ContainsPath(%q, %q) = false, want true", linkRoot, target)
	}
}

func TestContainsPathNormalizesWindowsDriveAliases(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows drive alias handling is Windows-specific")
	}
	root := t.TempDir()
	child := filepath.Join(root, "pkg", "file.go")
	alias := `\` + child
	if strings.HasPrefix(child, `\\`) {
		t.Skip("test requires a drive-qualified path, not a UNC path")
	}

	if !ContainsPath(root, alias) {
		t.Fatalf("ContainsPath(%q, %q) = false, want true", root, alias)
	}
	if !ContainsPath(strings.ToUpper(root), strings.ToLower(child)) {
		t.Fatalf("ContainsPath should compare Windows drive paths case-insensitively")
	}
}
