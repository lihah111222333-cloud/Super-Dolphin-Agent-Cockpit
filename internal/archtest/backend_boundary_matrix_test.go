package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

var backendBoundaryMatrixRuleIDs = []archtest.BoundaryRuleID{
	"contract_dto_no_framework_imports",
	"contract_reverse_pollution",
	"module_horizontal_deep_import",
	"module_no_outer_implementation_imports",
	"mcp_sidecar_narrow_import_surface",
	"platform_no_module",
	"provider_external_import_surface",
	"provider_unified_no_concrete_imports",
	"store_sqlc_store_platform_only",
}

type boundaryViolationFixture struct {
	name     string
	ruleID   archtest.BoundaryRuleID
	relPath  string
	source   string
	wantHits []string
}

var backendBoundaryKnownViolationFixtures = []boundaryViolationFixture{
	{
		name:    "contract_dto_framework_import",
		ruleID:  "contract_dto_no_framework_imports",
		relPath: "internal/dto/future/payload.go",
		source: `package fixture
import _ "github.com/creachadair/jrpc2"
`,
		wantHits: []string{"internal/dto/future/payload.go", "github.com/creachadair/jrpc2"},
	},
	{
		name:    "contract_reverse_pollution",
		ruleID:  "contract_reverse_pollution",
		relPath: "internal/contract/leak.go",
		source: `package fixture
import (
	_ "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/thread"
	_ "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/thread"
	_ "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp"
	_ "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch"
	_ "github.com/lihah111222333-cloud/super-dolphin-agent/frontend-app/src"
)
`,
		wantHits: []string{"internal/store/thread", "internal/module/thread", "internal/provider/codexapp", "cmd/mcp-orch", "frontend-app"},
	},
	{
		name:    "contract_closed_allowlist_is_exact",
		ruleID:  "contract_reverse_pollution",
		relPath: "internal/contract/leak.go",
		source: `package fixture
import _ "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent/futureimpl"
`,
		wantHits: []string{"internal/dto/agent/futureimpl"},
	},
	{
		name:    "module_horizontal_deep_import",
		ruleID:  "module_horizontal_deep_import",
		relPath: "internal/module/thread/service.go",
		source: `package fixture
import _ "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/prompt/intent"
`,
		wantHits: []string{"internal/module/thread/service.go:2:10 imports github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/prompt/intent"},
	},
	{
		name:    "module_outer_implementation_dependency",
		ruleID:  "module_no_outer_implementation_imports",
		relPath: "internal/module/future/service.go",
		source: `package fixture
import (
	_ "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp"
	_ "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	_ "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/agent-runtime"
)
`,
		wantHits: []string{"internal/provider/codexapp", "internal/mcpserver/common", "cmd/agent-runtime"},
	},
	{
		name:    "provider_external_import_not_registered",
		ruleID:  "provider_external_import_surface",
		relPath: "internal/provider/future/runtime.go",
		source: `package fixture
import _ "example.com/unapproved/provider"
`,
		wantHits: []string{"example.com/unapproved/provider"},
	},
	{
		name:    "unified_imports_concrete_provider",
		ruleID:  "provider_unified_no_concrete_imports",
		relPath: "internal/provider/unified/runtime.go",
		source: `package fixture
import _ "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/future"
`,
		wantHits: []string{"internal/provider/future"},
	},
	{
		name:    "mcp_sidecar_direct_module_dependency",
		ruleID:  "mcp_sidecar_narrow_import_surface",
		relPath: "cmd/mcp-orch/main.go",
		source: `package fixture
import _ "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/thread"
`,
		wantHits: []string{"cmd/mcp-orch/main.go:2:10 imports", "internal/module/thread"},
	},
	mcpRPCHostImportFixture(),
	{
		name:    "platform_module_reverse_dependency",
		ruleID:  "platform_no_module",
		relPath: "internal/platform/toolbridge/handler.go",
		source: `package fixture
import _ "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/thread"
`,
		wantHits: []string{"internal/platform/toolbridge/handler.go:2:10 imports", "internal/module/thread"},
	},
	{
		name:    "sqlc_outside_persistence",
		ruleID:  "store_sqlc_store_platform_only",
		relPath: "internal/module/thread/service.go",
		source: `package fixture
import _ "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
`,
		wantHits: []string{"internal/module/thread/service.go:2:10 imports", "internal/store/sqlc"},
	},
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

func TestProviderExternalImportSurfaceAllowsRegisteredModuleSubpackages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.go")
	if err := os.WriteFile(path, []byte(`package fixture
import _ "golang.org/x/sync/errgroup"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	violations, err := archtest.EvaluateBackendBoundaryFile(
		path,
		"internal/provider/future/runtime.go",
		archtest.DefaultBackendBoundaryRegistry(),
		"provider_external_import_surface",
	)
	if err != nil {
		t.Fatalf("evaluate registered provider module subpackage: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("registered provider module subpackage must be allowed: %v", violations)
	}
}

func TestBackendBoundaryMatrixFixturesRejectKnownViolations(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	for _, tc := range backendBoundaryKnownViolationFixtures {
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

func TestBackendBoundaryViolationUsesPhysicalImportPosition(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "fixture.go")
	source := `package fixture

//line generated.go:900
import _ "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/thread"
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	violations, err := archtest.EvaluateBackendBoundaryFile(
		path,
		"internal/contract/leak.go",
		archtest.DefaultBackendBoundaryRegistry(),
		"contract_reverse_pollution",
	)
	if err != nil {
		t.Fatalf("EvaluateBackendBoundaryFile(): %v", err)
	}
	got := strings.Join(violations, "\n")
	if !strings.Contains(got, "internal/contract/leak.go:4:10 imports") {
		t.Fatalf("violation does not use physical import position:\n%s", got)
	}
}

func mcpRPCHostImportFixture() boundaryViolationFixture {
	return boundaryViolationFixture{
		name:    "mcp_sidecar_rpc_host_import",
		ruleID:  "mcp_sidecar_narrow_import_surface",
		relPath: "cmd/mcp-orch/main.go",
		source: `package fixture
import _ "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc/server"
`,
		wantHits: []string{"cmd/mcp-orch/main.go:2:10 imports", "internal/platform/rpc/server"},
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
