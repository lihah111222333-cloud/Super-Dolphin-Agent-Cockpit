package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentifierGuard(t *testing.T) {
	t.Parallel()

	allViolations := filterViolationsByKind(CheckAll(CheckOptions{
		RepoRoot:  repoRootForGuardTests(t),
		ScanRoots: DefaultScanRoots(),
		SkipDirs:  DefaultSkipDirs(),
	}), ViolationIdentifier)

	// 测试文件的命名下划线由统一冻结棘轮管理，此处只检查生产文件。
	var violations []Violation
	for _, v := range allViolations {
		if !IsTestFile(v.File) {
			violations = append(violations, v)
		}
	}
	if len(violations) == 0 {
		return
	}

	t.Fatalf("identifier guard violations (%d):\n%s", len(violations), formatViolations(violations))
}

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
