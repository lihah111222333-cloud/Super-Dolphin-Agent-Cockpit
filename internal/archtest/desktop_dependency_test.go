package archtest_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestDesktopDependenciesStayOutOfCoreRuntime(t *testing.T) {
	root := repoRoot(t)
	var violations []string
	for _, file := range parseImportFiles(t, root, "internal/module", "internal/provider", "internal/platform") {
		if strings.HasPrefix(file.RelPath, "internal/platform/ui/") {
			continue
		}
		for _, imp := range file.Imports {
			if strings.HasPrefix(imp, "github.com/wailsapp/wails") {
				violations = append(violations, fmt.Sprintf("%s imports Wails dependency %s", file.RelPath, imp))
			}
		}
	}
	failIfViolations(t, violations)
}
