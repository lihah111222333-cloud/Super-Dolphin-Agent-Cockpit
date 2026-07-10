package archtest_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

func TestBackendBoundaryRegistryOwnsCanonicalDependencyRules(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	for _, id := range []archtest.BoundaryRuleID{
		"store_dependency_surface",
		"fx_assembly_scope",
		"mcpserver_orch_family",
		"mcpserver_ida_family",
		"hooks_no_mcpcontrol",
		"mcpcontrol_no_hooks",
		"hooks_no_platform_db",
	} {
		if _, ok := registry.Rule(id); !ok {
			t.Errorf("canonical backend boundary rule %q is not registered", id)
		}
	}
}

func TestCanonicalDependencyRulesRejectProceduralBoundaryViolations(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	cases := []struct {
		name    string
		ruleID  archtest.BoundaryRuleID
		relPath string
		imp     string
	}{
		{name: "store sibling", ruleID: "store_dependency_surface", relPath: "internal/store/thread/store.go", imp: "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"},
		{name: "fx outside assembly", ruleID: "fx_assembly_scope", relPath: "internal/module/thread/service.go", imp: "go.uber.org/fx"},
		{name: "orch server imports lsp tool", ruleID: "mcpserver_orch_family", relPath: "cmd/mcp-orch/server.go", imp: "github.com/anthropic-ai/super-agent-v3/internal/tool/lsp"},
		{name: "ida server imports orchestration tool", ruleID: "mcpserver_ida_family", relPath: "cmd/mcp-ida/server.go", imp: "github.com/anthropic-ai/super-agent-v3/internal/tool/orchestration"},
		{name: "hooks imports mcpcontrol", ruleID: "hooks_no_mcpcontrol", relPath: "internal/platform/hooks/runner.go", imp: "github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"},
		{name: "mcpcontrol imports hooks", ruleID: "mcpcontrol_no_hooks", relPath: "internal/platform/mcpcontrol/server.go", imp: "github.com/anthropic-ai/super-agent-v3/internal/platform/hooks"},
		{name: "hooks test imports db", ruleID: "hooks_no_platform_db", relPath: "internal/platform/hooks/runner_test.go", imp: "github.com/anthropic-ai/super-agent-v3/internal/platform/db"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture.go")
			source := "package fixture\n\nimport _ " + strconv.Quote(tc.imp) + "\n"
			if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			violations, err := archtest.EvaluateBackendBoundaryFile(path, tc.relPath, registry, tc.ruleID)
			if err != nil {
				t.Fatalf("EvaluateBackendBoundaryFile(): %v", err)
			}
			if got := strings.Join(violations, "\n"); !strings.Contains(got, "rule="+string(tc.ruleID)) {
				t.Fatalf("fixture did not trigger %q:\n%s", tc.ruleID, got)
			}
		})
	}
}

func TestStoreDependencyRulePreservesAuditedImports(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	cases := []struct {
		name    string
		relPath string
		imp     string
	}{
		{name: "root aggregates store packages", relPath: "internal/store/module.go", imp: "github.com/anthropic-ai/super-agent-v3/internal/store/thread"},
		{name: "package module imports fx", relPath: "internal/store/thread/module.go", imp: "go.uber.org/fx"},
		{name: "package module imports pool", relPath: "internal/store/thread/module.go", imp: "github.com/jackc/pgx/v5/pgxpool"},
		{name: "same package subtree", relPath: "internal/store/thread/store.go", imp: "github.com/anthropic-ai/super-agent-v3/internal/store/thread/internal/model"},
		{name: "registered platform port", relPath: "internal/store/thread/store.go", imp: "github.com/anthropic-ai/super-agent-v3/internal/platform/db"},
		{name: "registered pgtype", relPath: "internal/store/thread/store.go", imp: "github.com/jackc/pgx/v5/pgtype"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture.go")
			source := "package fixture\n\nimport _ " + strconv.Quote(tc.imp) + "\n"
			if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			violations, err := archtest.EvaluateBackendBoundaryFile(path, tc.relPath, registry, "store_dependency_surface")
			if err != nil {
				t.Fatalf("EvaluateBackendBoundaryFile(): %v", err)
			}
			if len(violations) != 0 {
				t.Fatalf("audited store import was rejected: %v", violations)
			}
		})
	}
}

func TestStoreDependencyRuleKeepsExternalAllowancesExact(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	cases := []struct {
		relPath string
		imp     string
	}{
		{relPath: "internal/store/thread/module.go", imp: "go.uber.org/fx/internal"},
		{relPath: "internal/store/thread/store.go", imp: "github.com/jackc/pgx/v5/pgtype/extra"},
		{relPath: "internal/store/thread/store.go", imp: "corp/persistence"},
	}
	for _, tc := range cases {
		path := filepath.Join(t.TempDir(), "fixture.go")
		source := "package fixture\n\nimport _ " + strconv.Quote(tc.imp) + "\n"
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		violations, err := archtest.EvaluateBackendBoundaryFile(path, tc.relPath, registry, "store_dependency_surface")
		if err != nil {
			t.Fatalf("EvaluateBackendBoundaryFile(): %v", err)
		}
		if len(violations) == 0 {
			t.Errorf("external store allowance widened to %s", tc.imp)
		}
	}
}

func TestFXAssemblyScopesUseTypedPathMatchers(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	cases := []struct {
		name      string
		relPath   string
		wantAllow bool
	}{
		{name: "nested module file", relPath: "internal/module/thread/module.go", wantAllow: true},
		{name: "direct command entrypoint", relPath: "cmd/agent-terminal/main.go", wantAllow: true},
		{name: "direct command helper", relPath: "cmd/agent-terminal/frontend.go", wantAllow: false},
		{name: "another direct command helper", relPath: "cmd/agent-terminal/helper.go", wantAllow: false},
		{name: "nested non module file", relPath: "internal/module/thread/service.go", wantAllow: false},
		{name: "nested command implementation", relPath: "cmd/agent-terminal/internal/main.go", wantAllow: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture.go")
			if err := os.WriteFile(path, []byte("package fixture\n\nimport _ \"go.uber.org/fx\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			violations, err := archtest.EvaluateBackendBoundaryFile(path, tc.relPath, registry, "fx_assembly_scope")
			if err != nil {
				t.Fatalf("EvaluateBackendBoundaryFile(): %v", err)
			}
			if tc.wantAllow && len(violations) != 0 {
				t.Fatalf("typed Fx scope rejected %s: %v", tc.relPath, violations)
			}
			if !tc.wantAllow && len(violations) == 0 {
				t.Fatalf("typed Fx scope allowed %s", tc.relPath)
			}
		})
	}
}

func TestFXAssemblyScopeRejectsUnregisteredMatcher(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	rule := mustCanonicalBackendBoundaryRule(t, registry, "fx_assembly_scope")
	rule.ScopeAllow[1].Scope = "fx_renamed_module_file"
	replaceCanonicalBackendBoundaryRule(t, &registry, rule)

	violations := strings.Join(archtest.ValidateBackendBoundaryRegistry(registry), "\n")
	if !strings.Contains(violations, "scope_allow file_pattern is not a registered scope") {
		t.Fatalf("unregistered typed Fx scope must fail closed:\n%s", violations)
	}
}

func TestFXAssemblyRuleRejectsUnsupportedGlob(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	rule := mustCanonicalBackendBoundaryRule(t, registry, "fx_assembly_scope")
	rule.FilePatterns = append(rule.FilePatterns, "internal/**/modules.go")
	replaceCanonicalBackendBoundaryRule(t, &registry, rule)

	violations := strings.Join(archtest.ValidateBackendBoundaryRegistry(registry), "\n")
	if !strings.Contains(violations, "unsupported file pattern syntax") {
		t.Fatalf("unsupported Fx glob must fail closed:\n%s", violations)
	}
}
