package archtest_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestMCPFamilyIsolation(t *testing.T) {
	root := repoRoot(t)
	cases := []struct {
		name      string
		relPkg    string
		forbidden []string
	}{
		{name: "mcp_lsp", relPkg: "cmd/mcp-lsp", forbidden: []string{"internal/tool/ida", "internal/tool/orchestration"}},
		{name: "mcp_orch", relPkg: "cmd/mcp-orch", forbidden: []string{"internal/tool/lsp", "internal/tool/ida"}},
		{name: "mcp_ida", relPkg: "cmd/mcp-ida", forbidden: []string{"internal/tool/lsp", "internal/tool/orchestration"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if len(walkGoFiles(t, root, tc.relPkg)) == 0 {
				t.Skip("directory not yet created")
			}
			deps := goListDeps(t, root, tc.relPkg)
			var violations []string
			for _, dep := range deps {
				for _, forbidden := range tc.forbidden {
					prefix := internalPrefix(forbidden)
					if dep == prefix || strings.HasPrefix(dep, prefix+"/") {
						violations = append(violations, fmt.Sprintf("%s depends on %s", tc.relPkg, dep))
					}
				}
			}
			failIfViolations(t, violations)
		})
	}
}
