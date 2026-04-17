package archtest_test

import (
	"fmt"
	"strings"
	"testing"
)

const mcpOrchRelPkg = "cmd/" + "mcp-orch"

func TestMCPFamilyIsolation(t *testing.T) {
	root := repoRoot(t)
	cases := []struct {
		name      string
		relPkg    string
		forbidden []string
	}{
		{name: "mcp_lsp", relPkg: "cmd/mcp-lsp", forbidden: []string{"cmd/mcp-orch", "cmd/mcp-ida"}},
		{name: "mcp_orch", relPkg: mcpOrchRelPkg, forbidden: []string{"cmd/mcp-lsp", "cmd/mcp-ida"}},
		{name: "mcp_ida", relPkg: "cmd/mcp-ida", forbidden: []string{"cmd/mcp-lsp", "cmd/mcp-orch"}},
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
