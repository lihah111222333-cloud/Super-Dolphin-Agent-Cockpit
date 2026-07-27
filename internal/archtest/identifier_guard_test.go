package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoRootForGuardTests(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd(): %v", err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("go.mod not found from %s: %v", root, err)
	}
	return root
}

func filterViolationsByKind(violations []Violation, kind ViolationKind) []Violation {
	filtered := make([]Violation, 0, len(violations))
	for _, violation := range violations {
		if violation.Kind == kind {
			filtered = append(filtered, violation)
		}
	}
	return filtered
}

func formatViolations(violations []Violation) string {
	lines := make([]string, 0, len(violations))
	for _, violation := range violations {
		lines = append(lines, violation.String())
	}
	return strings.Join(lines, "\n")
}
