package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

var backendBoundaryMatrixRuleIDs = []archtest.BoundaryRuleID{
	"contract_reverse_pollution",
	"module_horizontal_deep_import",
	"mcp_sidecar_narrow_import_surface",
	"platform_no_module",
	"store_sqlc_store_platform_only",
}

func TestBackendBoundaryMatrix(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	if violations := archtest.ValidateBackendBoundaryRegistry(registry); len(violations) > 0 {
		t.Fatalf("backend boundary registry is invalid:\n%s", strings.Join(violations, "\n"))
	}
	evaluation, err := archtest.EvaluateBackendBoundary(repoRoot(t), registry, backendBoundaryMatrixRuleIDs...)
	if err != nil {
		t.Fatalf("EvaluateBackendBoundary(): %v", err)
	}
	failIfViolations(t, evaluation.Violations)
}

func TestBackendBoundaryMatrixRejectsUnauditedAllowlist(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	rule := mustCanonicalBackendBoundaryRule(t, registry, "mcp_sidecar_narrow_import_surface")
	rule.Allow = append(rule.Allow, archtest.BoundaryImportPolicy{
		Owner:        rule.Owner,
		ImportPrefix: "internal/app",
		Reason:       "synthetic unaudited allowlist fixture",
	})
	replaceCanonicalBackendBoundaryRule(t, &registry, rule)

	violations := strings.Join(archtest.ValidateBackendBoundaryRegistry(registry), "\n")
	if !strings.Contains(violations, "file_pattern is empty") {
		t.Fatalf("ValidateBackendBoundaryRegistry() did not reject missing file pattern:\n%s", violations)
	}
}

func TestBackendBoundaryMatrixRejectsGenericStatefulSidecarAllowlist(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	rule := mustCanonicalBackendBoundaryRule(t, registry, "mcp_sidecar_narrow_import_surface")
	rule.Allow = append(rule.Allow, archtest.BoundaryImportPolicy{
		Owner:        rule.Owner,
		FilePattern:  "cmd/mcp-lsp/**/*.go",
		ImportPrefix: "internal/platform/db",
		Reason:       "SQLite lifecycle primitives shared by sidecars",
	})
	replaceCanonicalBackendBoundaryRule(t, &registry, rule)

	violations := strings.Join(archtest.ValidateBackendBoundaryRegistry(registry), "\n")
	if !strings.Contains(violations, "stateful sidecar allowance must name its sidecar") {
		t.Fatalf("ValidateBackendBoundaryRegistry() did not reject generic stateful sidecar reason:\n%s", violations)
	}
}

func TestBackendBoundaryMatrixFixturesRejectKnownViolations(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	cases := []struct {
		name     string
		ruleID   archtest.BoundaryRuleID
		relPath  string
		source   string
		wantHits []string
	}{
		{
			name:    "contract_reverse_pollution",
			ruleID:  "contract_reverse_pollution",
			relPath: "internal/contract/leak.go",
			source: `package fixture
import (
	_ "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	_ "github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	_ "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	_ "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch"
	_ "github.com/anthropic-ai/super-agent-v3/frontend-app/src"
)
`,
			wantHits: []string{"internal/store/thread", "internal/module/thread", "internal/provider/codexapp", "cmd/mcp-orch", "frontend-app"},
		},
		{
			name:    "module_horizontal_deep_import",
			ruleID:  "module_horizontal_deep_import",
			relPath: "internal/module/thread/service.go",
			source: `package fixture
import _ "github.com/anthropic-ai/super-agent-v3/internal/module/prompt/intent"
`,
			wantHits: []string{"internal/module/thread/service.go imports github.com/anthropic-ai/super-agent-v3/internal/module/prompt/intent"},
		},
		{
			name:    "mcp_sidecar_direct_module_dependency",
			ruleID:  "mcp_sidecar_narrow_import_surface",
			relPath: "cmd/mcp-orch/main.go",
			source: `package fixture
import _ "github.com/anthropic-ai/super-agent-v3/internal/module/thread"
`,
			wantHits: []string{"cmd/mcp-orch/main.go imports", "internal/module/thread"},
		},
		{
			name:    "platform_module_reverse_dependency",
			ruleID:  "platform_no_module",
			relPath: "internal/platform/toolbridge/handler.go",
			source: `package fixture
import _ "github.com/anthropic-ai/super-agent-v3/internal/module/thread"
`,
			wantHits: []string{"internal/platform/toolbridge/handler.go imports", "internal/module/thread"},
		},
		{
			name:    "sqlc_outside_persistence",
			ruleID:  "store_sqlc_store_platform_only",
			relPath: "internal/module/thread/service.go",
			source: `package fixture
import _ "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
`,
			wantHits: []string{"internal/module/thread/service.go imports", "internal/store/sqlc"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture.go")
			if err := os.WriteFile(path, []byte(tc.source), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			violations, err := archtest.EvaluateBackendBoundaryFile(path, tc.relPath, registry, tc.ruleID)
			if err != nil {
				t.Fatalf("EvaluateBackendBoundaryFile(): %v", err)
			}
			got := strings.Join(violations, "\n")
			for _, want := range tc.wantHits {
				if !strings.Contains(got, want) {
					t.Fatalf("EvaluateBackendBoundaryFile() missing %q in:\n%s", want, got)
				}
			}
		})
	}
}

func mustCanonicalBackendBoundaryRule(t *testing.T, registry archtest.BackendBoundaryRegistry, id archtest.BoundaryRuleID) archtest.BackendBoundaryRule {
	t.Helper()
	rule, ok := registry.Rule(id)
	if !ok {
		t.Fatalf("canonical backend boundary rule %q is not registered", id)
	}
	return rule
}

func replaceCanonicalBackendBoundaryRule(t *testing.T, registry *archtest.BackendBoundaryRegistry, replacement archtest.BackendBoundaryRule) {
	t.Helper()
	for i, rule := range registry.Rules {
		if rule.ID == replacement.ID {
			registry.Rules[i] = replacement
			return
		}
	}
	t.Fatalf("canonical backend boundary rule %q is not registered", replacement.ID)
}
