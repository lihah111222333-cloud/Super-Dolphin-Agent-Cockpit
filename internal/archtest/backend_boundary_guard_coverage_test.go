package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackendBoundaryGuardsCoverProductionTree(t *testing.T) {
	t.Parallel()

	registry := DefaultBackendBoundaryRegistry()
	evaluation, err := EvaluateBackendBoundary(repoRootForGuardTests(t), registry)
	if err != nil {
		t.Fatalf("evaluate production backend boundaries: %v", err)
	}
	if evaluation.CandidateFiles == 0 {
		t.Fatal("backend boundary registry matched zero production Go candidates")
	}
	for _, rule := range registry.Rules {
		if evaluation.ByRule[rule.ID] == 0 {
			t.Fatalf("backend boundary rule %q matched zero production files", rule.ID)
		}
	}
	if len(evaluation.Violations) > 0 {
		t.Fatalf("backend boundary production violations:\n%s", strings.Join(evaluation.Violations, "\n"))
	}
}

func TestBackendBoundaryGuardsFailClosed(t *testing.T) {
	t.Parallel()

	registry := DefaultBackendBoundaryRegistry()
	if _, err := EvaluateBackendBoundary(filepath.Join(t.TempDir(), "missing"), registry); err == nil {
		t.Fatal("missing backend boundary root must return an error")
	}
	if _, err := EvaluateBackendBoundary(t.TempDir(), registry, registry.Rules[0].ID); err == nil {
		t.Fatal("empty backend boundary candidate directory must return an error")
	}

	malformedRoot := t.TempDir()
	malformedRel := "internal/contract/broken.go"
	malformedPath := writeBackendBoundaryFixture(t, malformedRoot, malformedRel, "package contract\nfunc (\n")
	if _, err := EvaluateBackendBoundaryFile(malformedPath, malformedRel, registry, "contract_reverse_pollution"); err == nil {
		t.Fatal("malformed Go source must return an error")
	}
	if _, err := EvaluateBackendBoundary(t.TempDir(), registry, "unknown_backend_boundary_rule"); err == nil {
		t.Fatal("unknown backend boundary rule must return an error")
	}
}

func TestProviderBoundaryDescriptorsCoverProductionTree(t *testing.T) {
	t.Parallel()

	registry := DefaultBackendBoundaryRegistry()
	ruleIDs := []BoundaryRuleID{"provider_no_store", "provider_no_platform_db"}
	root := repoRootForGuardTests(t)
	paths, err := collectBackendBoundaryGoFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	providerFiles := 0
	for _, path := range paths {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("relative provider path for %s: %v", path, err)
		}
		rel = filepath.ToSlash(rel)
		if !strings.HasPrefix(rel, "internal/provider/") || strings.HasSuffix(rel, "_test.go") {
			continue
		}
		providerFiles++
		for _, ruleID := range ruleIDs {
			rule, ok := registry.Rule(ruleID)
			if !ok {
				t.Fatalf("canonical provider boundary rule %q is missing", ruleID)
			}
			if !matchesAnyBackendBoundaryPattern(rule.FilePatterns, rel) {
				t.Errorf("provider production file %q is missing from rule %q", rel, ruleID)
			}
		}
	}
	if providerFiles == 0 {
		t.Fatal("provider production tree contains zero Go files")
	}
}

func TestMCPSidecarBoundaryDescriptorsStayAligned(t *testing.T) {
	t.Parallel()

	rule, ok := DefaultBackendBoundaryRegistry().Rule("mcp_sidecar_narrow_import_surface")
	if !ok {
		t.Fatal("canonical MCP sidecar boundary rule is missing")
	}
	unregistered, err := unregisteredMCPSidecarDirectories(repoRootForGuardTests(t), rule)
	if err != nil {
		t.Fatal(err)
	}
	if len(unregistered) > 0 {
		t.Fatalf("MCP sidecar directories are missing from canonical boundary rule: %s", strings.Join(unregistered, ", "))
	}
	filePatterns := make(map[string]bool, len(rule.FilePatterns))
	for _, pattern := range rule.FilePatterns {
		filePatterns[pattern] = true
	}
	dependencyPatterns := make(map[string]bool, len(rule.DependencyPackages))
	for _, pkg := range rule.DependencyPackages {
		dependencyPatterns[pkg+"/**/*.go"] = true
	}
	allowPatterns := make(map[string]bool, len(rule.Allow))
	for _, policy := range rule.Allow {
		allowPatterns[policy.FilePattern] = true
	}
	assertBoundaryPatternSetContains(t, filePatterns, dependencyPatterns, "dependency packages")
	assertBoundaryPatternSetContains(t, filePatterns, allowPatterns, "import allowances")
	assertBoundaryPatternSetContains(t, dependencyPatterns, filePatterns, "rule file patterns")
	assertBoundaryPatternSetContains(t, allowPatterns, filePatterns, "rule file patterns")
}

func TestMCPSidecarBoundaryRejectsUnregisteredDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "mcp-shadow"), 0o755); err != nil {
		t.Fatal(err)
	}
	rule, ok := DefaultBackendBoundaryRegistry().Rule("mcp_sidecar_narrow_import_surface")
	if !ok {
		t.Fatal("canonical MCP sidecar boundary rule is missing")
	}
	unregistered, err := unregisteredMCPSidecarDirectories(root, rule)
	if err != nil {
		t.Fatal(err)
	}
	if len(unregistered) != 1 || unregistered[0] != "cmd/mcp-shadow" {
		t.Fatalf("unregistered MCP sidecar discovery got %v", unregistered)
	}
}

func assertBoundaryPatternSetContains(t *testing.T, expected, actual map[string]bool, actualLabel string) {
	t.Helper()
	for pattern := range expected {
		if !actual[pattern] {
			t.Errorf("MCP sidecar pattern %q is missing from %s", pattern, actualLabel)
		}
	}
}

// unregisteredMCPSidecarDirectories 将实际 cmd/mcp-* 目录与 canonical 规则覆盖做精确对账。
func unregisteredMCPSidecarDirectories(root string, rule BackendBoundaryRule) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		return nil, err
	}
	registered := make(map[string]bool, len(rule.FilePatterns))
	for _, pattern := range rule.FilePatterns {
		registered[strings.TrimSuffix(pattern, "/**/*.go")] = true
	}
	var unregistered []string
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "mcp-") {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("cmd", entry.Name()))
		if !registered[rel] {
			unregistered = append(unregistered, rel)
		}
	}
	return unregistered, nil
}

func TestBackendBoundaryGuardFixturesRejectKnownViolations(t *testing.T) {
	t.Parallel()

	registry := DefaultBackendBoundaryRegistry()
	cases := []struct {
		name    string
		ruleID  BoundaryRuleID
		relPath string
		source  string
	}{
		{
			name:    "contract_to_module",
			ruleID:  "contract_reverse_pollution",
			relPath: "internal/contract/leak.go",
			source:  "package contract\n\nimport _ \"github.com/anthropic-ai/super-agent-v3/internal/module/thread\"\n",
		},
		{
			name:    "module_sibling_deep_import",
			ruleID:  "module_horizontal_deep_import",
			relPath: "internal/module/thread/service.go",
			source:  "package thread\n\nimport _ \"github.com/anthropic-ai/super-agent-v3/internal/module/prompt/intent\"\n",
		},
		{
			name:    "mcp_lsp_orchestration_only_import",
			ruleID:  "mcp_sidecar_narrow_import_surface",
			relPath: "cmd/mcp-lsp/main.go",
			source:  "package main\n\nimport _ \"github.com/anthropic-ai/super-agent-v3/internal/platform/notify\"\n",
		},
		{
			name:    "provider_shared_to_store",
			ruleID:  "provider_no_store",
			relPath: "internal/provider/shared/leak.go",
			source:  "package shared\n\nimport _ \"github.com/anthropic-ai/super-agent-v3/internal/store\"\n",
		},
		{
			name:    "provider_shared_to_platform_db",
			ruleID:  "provider_no_platform_db",
			relPath: "internal/provider/shared/leak.go",
			source:  "package shared\n\nimport _ \"github.com/anthropic-ai/super-agent-v3/internal/platform/db\"\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := writeBackendBoundaryFixture(t, root, tc.relPath, tc.source)
			violations, err := EvaluateBackendBoundaryFile(path, tc.relPath, registry, tc.ruleID)
			if err != nil {
				t.Fatalf("evaluate fixture: %v", err)
			}
			if !strings.Contains(strings.Join(violations, "\n"), "rule="+string(tc.ruleID)) {
				t.Fatalf("fixture %q did not trigger rule %q: %v", tc.relPath, tc.ruleID, violations)
			}
		})
	}
}

func TestEvaluateBackendBoundaryFileReportsImportPosition(t *testing.T) {
	t.Parallel()

	registry := DefaultBackendBoundaryRegistry()
	cases := []struct {
		name       string
		source     string
		wantPrefix string
	}{
		{
			name:       "single_import",
			source:     "package contract\n\nimport _ \"github.com/anthropic-ai/super-agent-v3/internal/module/thread\"\n",
			wantPrefix: "internal/contract/leak.go:3:10 imports ",
		},
		{
			name:       "import_block",
			source:     "package contract\n\nimport (\n    _ \"github.com/anthropic-ai/super-agent-v3/internal/module/thread\"\n)\n",
			wantPrefix: "internal/contract/leak.go:4:7 imports ",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			relPath := "internal/contract/leak.go"
			path := writeBackendBoundaryFixture(t, t.TempDir(), relPath, tc.source)
			violations, err := EvaluateBackendBoundaryFile(path, relPath, registry, "contract_reverse_pollution")
			if err != nil {
				t.Fatalf("evaluate fixture: %v", err)
			}
			if len(violations) != 1 {
				t.Fatalf("violations = %v, want exactly one", violations)
			}
			if !strings.HasPrefix(violations[0], tc.wantPrefix) {
				t.Fatalf("violation = %q, want prefix %q", violations[0], tc.wantPrefix)
			}
		})
	}
}

func TestPkgNoInternalImportsRuleRejectsRepositoryInternals(t *testing.T) {
	t.Parallel()

	relPath := "pkg/logger/leak.go"
	source := "package logger\n\nimport (\n    _ \"github.com/anthropic-ai/super-agent-v3/internal/platform/config\"\n    _ \"github.com/anthropic-ai/super-agent-v3/cmd/agent-runtime\"\n    _ \"log/slog\"\n)\n"
	path := writeBackendBoundaryFixture(t, t.TempDir(), relPath, source)
	violations, err := EvaluateBackendBoundaryFile(path, relPath, DefaultBackendBoundaryRegistry(), "pkg_no_internal_imports")
	if err != nil {
		t.Fatalf("evaluate pkg boundary fixture: %v", err)
	}
	if len(violations) != 2 {
		t.Fatalf("violations = %v, want internal and cmd imports only", violations)
	}
	joined := strings.Join(violations, "\n")
	for _, forbidden := range []string{
		"github.com/anthropic-ai/super-agent-v3/internal/platform/config",
		"github.com/anthropic-ai/super-agent-v3/cmd/agent-runtime",
	} {
		if !strings.Contains(joined, forbidden) {
			t.Errorf("violations missing forbidden import %q: %v", forbidden, violations)
		}
	}
	if strings.Contains(joined, "log/slog") {
		t.Fatalf("standard library import was rejected: %v", violations)
	}
}

func writeBackendBoundaryFixture(t *testing.T, root, relPath, source string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create backend boundary fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write backend boundary fixture: %v", err)
	}
	return path
}
