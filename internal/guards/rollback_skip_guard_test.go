package guards

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoTestsDoNotContainRollbackSkipMarkers(t *testing.T) {
	root := findRepositoryRoot(t)
	var offenders []string

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if shouldSkipRollbackMarkerDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		hasMarker, err := testFileContainsRollbackMarker(path, entry)
		if err != nil {
			return err
		}
		if hasMarker {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			offenders = append(offenders, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan Go tests for rollback skip markers: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("remove rollback skip markers from _test.go files: %s", strings.Join(offenders, ", "))
	}
}

func shouldSkipRollbackMarkerDir(name string) bool {
	switch name {
	case ".git", ".worktrees", ".workspace", "node_modules", "dist", ".build-cache":
		return true
	default:
		return false
	}
}

func testFileContainsRollbackMarker(path string, entry os.DirEntry) (bool, error) {
	if !strings.HasSuffix(entry.Name(), "_test.go") {
		return false, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	content := string(raw)
	return strings.Contains(content, rollbackSkipStartMarker()) || strings.Contains(content, rollbackSkipEndMarker()), nil
}

func rollbackSkipStartMarker() string { return "ROLLBACK_" + "SKIP_START" }

func rollbackSkipEndMarker() string { return "ROLLBACK_" + "SKIP_END" }

func findRepositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found while locating repository root")
		}
		dir = parent
	}
}
