package archtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

func TestSharedBudget(t *testing.T) {
	root := repoRoot(t)
	if !dirExists(root, "internal/platform/shared") {
		t.Skip("directory not yet created")
	}
	files := walkGoFiles(t, root, "internal/platform/shared")
	var total int
	var violations []string
	for _, absPath := range files {
		relPath, err := filepath.Rel(root, absPath)
		if err != nil {
			t.Fatalf("rel path for %s: %v", absPath, err)
		}
		relPath = filepath.ToSlash(relPath)
		data, err := os.ReadFile(absPath)
		if err != nil {
			t.Fatalf("read %s: %v", relPath, err)
		}
		lines := archtest.CountEffectiveLines(data)
		total += lines
		if lines > 500 {
			violations = append(violations, fmt.Sprintf("%s has %d effective lines > 500", relPath, lines))
		}
		for _, imp := range parseImports(t, absPath) {
			prefix := internalPrefix("internal/module/")
			if imp == prefix || strings.HasPrefix(imp, prefix+"/") {
				violations = append(violations, fmt.Sprintf("%s imports %s", relPath, imp))
			}
		}
	}
	if total > 2000 {
		violations = append(violations, fmt.Sprintf("internal/platform/shared has %d effective lines > 2000", total))
	}
	failIfViolations(t, violations)
}
