package archtest_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestSqlcBoundary(t *testing.T) {
	root := repoRoot(t)
	if !dirExists(root, "internal/store/sqlc") {
		t.Skip("directory not yet created")
	}
	prefix := internalPrefix("internal/store/sqlc")
	var violations []string
	for _, file := range parseImportFiles(t, root, "internal", "cmd") {
		for _, imp := range file.Imports {
			if imp != prefix && !strings.HasPrefix(imp, prefix+"/") {
				continue
			}
			if strings.HasPrefix(file.RelPath, "internal/store/") {
				continue
			}
			violations = append(violations, fmt.Sprintf("%s imports %s outside internal/store/*", file.RelPath, imp))
		}
	}
	failIfViolations(t, violations)
}
