package mirrorpath

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestUnsafeRelativeRejectsEscapesAndEmptySegments verifies mirror paths cannot escape roots.
func TestUnsafeRelativeRejectsEscapesAndEmptySegments(t *testing.T) {
	t.Parallel()

	unsafeValues := []string{"", ".", "..", "../x", "x/../y", "x//y", "/abs"}
	for _, rel := range unsafeValues {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			t.Parallel()

			if !UnsafeRelative(rel) {
				t.Fatalf("UnsafeRelative(%q) = false, want true", rel)
			}
		})
	}

	if UnsafeRelative("safe/file.txt") {
		t.Fatal("UnsafeRelative(safe/file.txt) = true, want false")
	}
}

// TestSafeRelativeReturnsSlashPathInsideRoot verifies safe paths are normalized for mirror manifests.
func TestSafeRelativeReturnsSlashPathInsideRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	got, err := SafeRelative(root, filepath.Join(root, "dir", "file.txt"))
	if err != nil {
		t.Fatalf("SafeRelative() error = %v", err)
	}
	if got != "dir/file.txt" {
		t.Fatalf("SafeRelative() = %q, want dir/file.txt", got)
	}

	_, err = SafeRelative(root, filepath.Join(root, "..", "outside.txt"))
	if err == nil || !strings.Contains(err.Error(), "escapes root") {
		t.Fatalf("SafeRelative(escape) error = %v, want escapes root", err)
	}
}

// TestForExistingSkillDirsVisitsSelectedThenExistingMirrors verifies mirror cleanup order.
func TestForExistingSkillDirsVisitsSelectedThenExistingMirrors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	selected := filepath.Join(root, "selected", "skill")
	mirror := filepath.Join(root, "mirror", "skill")
	if err := mkdirAll(selected, mirror); err != nil {
		t.Fatal(err)
	}

	var visited []string
	err := ForExistingSkillDirs([]string{filepath.Join(root, "mirror"), filepath.Join(root, "missing")}, "skill", selected, func(path string) error {
		visited = append(visited, filepath.Clean(path))
		return nil
	})
	if err != nil {
		t.Fatalf("ForExistingSkillDirs() error = %v", err)
	}
	want := []string{filepath.Clean(selected), filepath.Clean(mirror)}
	if !reflect.DeepEqual(visited, want) {
		t.Fatalf("ForExistingSkillDirs() visited = %#v, want %#v", visited, want)
	}
}

func mkdirAll(paths ...string) error {
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
	}
	return nil
}
