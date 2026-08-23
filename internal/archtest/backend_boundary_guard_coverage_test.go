package archtest

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runBackendBoundaryProductionTreeCoverage(t *testing.T, evaluation BoundaryEvaluation) {
	t.Helper()
	if evaluation.CandidateFiles == 0 {
		t.Fatal("backend boundary registry matched zero production Go candidates")
	}
	for _, rule := range DefaultBackendBoundaryRegistry().Rules {
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
	if _, err := EvaluateBackendBoundaryFile(malformedRoot, malformedPath, malformedRel, registry, "contract_reverse_pollution"); err == nil {
		t.Fatal("malformed Go source must return an error")
	}
	if _, err := EvaluateBackendBoundary(t.TempDir(), registry, "unknown_backend_boundary_rule"); err == nil {
		t.Fatal("unknown backend boundary rule must return an error")
	}
}

// TestTemporaryBackendBoundaryFixturesDeclareSQLCOwner enumerates every test
// function that combines a temporary root with the evaluator and requires the
// shared canonical SQLC fixture helper in that same fixture entry.
func TestTemporaryBackendBoundaryFixturesDeclareSQLCOwner(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	paths, err := filepath.Glob(filepath.Join(root, "internal", "archtest", "*_test.go"))
	if err != nil {
		t.Fatalf("enumerate backend boundary test files: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("backend boundary test enumeration found no Go tests")
	}
	for _, path := range paths {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read backend boundary test %s: %v", path, err)
		}
		missing, err := temporaryEvaluatorFixtureEntries(path, source)
		if err != nil {
			t.Fatalf("parse backend boundary test %s: %v", path, err)
		}
		for _, functionName := range missing {
			t.Errorf("%s: temporary evaluator fixture %s must call shared canonical SQLC fixture helper", filepath.ToSlash(filepath.Join("internal", "archtest", filepath.Base(path))), functionName)
		}
	}
}

func temporaryEvaluatorFixtureEntries(path string, source []byte) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return nil, err
	}
	var missing []string
	ast.Inspect(file, func(node ast.Node) bool {
		fn, ok := node.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		if temporaryEvaluatorNeedsOwner(fn) {
			missing = append(missing, fn.Name.Name)
		}
		return false
	})
	return missing, nil
}

func temporaryEvaluatorNeedsOwner(fn *ast.FuncDecl) bool {
	var hasTempRoot, hasEvaluator, hasSQLCOwner bool
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := backendBoundaryFixtureCallName(call)
		switch name {
		case "TempDir":
			hasTempRoot = true
		case "EvaluateBackendBoundary", "EvaluateBackendBoundaryFile":
			hasEvaluator = true
		case "writeCanonicalSQLCGuardFixture", "WriteCanonicalSQLCGuardFixture", "writeBackendBoundaryFixture":
			hasSQLCOwner = true
		}
		return true
	})
	return hasTempRoot && hasEvaluator && !hasSQLCOwner
}

func backendBoundaryFixtureCallName(call *ast.CallExpr) string {
	switch function := call.Fun.(type) {
	case *ast.SelectorExpr:
		return function.Sel.Name
	case *ast.Ident:
		return function.Name
	default:
		return ""
	}
}

func TestBackendBoundaryCollectorSkipsForeignWorktreesCachesAndTestFiles(t *testing.T) {
	root := t.TempDir()
	expected := writeBackendBoundaryFixture(t, root, "internal/contract/ok.go", "package contract\n")
	writeBackendBoundaryFixture(t, root, "internal/contract/ok_test.go", "package contract\n")
	for _, dir := range []string{".worktrees", ".workspace", ".build-cache"} {
		writeBackendBoundaryFixture(
			t,
			root,
			filepath.ToSlash(filepath.Join(dir, "foreign", "internal", "bad.go")),
			"package bad\n",
		)
	}

	paths, err := collectBackendBoundaryGoFiles(root)
	if err != nil {
		t.Fatalf("collect backend boundary Go files: %v", err)
	}
	if len(paths) != 1 || paths[0] != expected {
		t.Fatalf("collector should ignore foreign worktrees and caches, got %v", paths)
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
	commandRule, ok := DefaultBackendBoundaryRegistry().Rule("command_narrow_import_surface")
	if !ok {
		return nil, errors.New("canonical command boundary rule is missing")
	}
	for _, pattern := range commandRule.FilePatterns {
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
			source:  "package contract\n\nimport _ \"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/thread\"\n",
		},
		{
			name:    "module_sibling_deep_import",
			ruleID:  "module_horizontal_deep_import",
			relPath: "internal/module/thread/service.go",
			source:  "package thread\n\nimport _ \"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/prompt/intent\"\n",
		},
		{
			name:    "mcp_lsp_orchestration_only_import",
			ruleID:  "mcp_sidecar_narrow_import_surface",
			relPath: "cmd/mcp-lsp/main.go",
			source:  "package main\n\nimport _ \"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/notify\"\n",
		},
		{
			name:    "agent_runtime_to_module",
			ruleID:  "command_narrow_import_surface",
			relPath: "cmd/agent-runtime/main.go",
			source:  "package main\n\nimport _ \"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/thread\"\n",
		},
		{
			name:    "dto_to_provider",
			ruleID:  "internal_support_narrow_import_surface",
			relPath: "internal/dto/agent/leak.go",
			source:  "package agent\n\nimport _ \"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared\"\n",
		},
		{
			name:    "provider_shared_to_store",
			ruleID:  "provider_no_store",
			relPath: "internal/provider/shared/leak.go",
			source:  "package shared\n\nimport _ \"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store\"\n",
		},
		{
			name:    "provider_shared_to_platform_db",
			ruleID:  "provider_no_platform_db",
			relPath: "internal/provider/shared/leak.go",
			source:  "package shared\n\nimport _ \"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db\"\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := writeBackendBoundaryFixture(t, root, tc.relPath, tc.source)
			violations, err := EvaluateBackendBoundaryFile(root, path, tc.relPath, registry, tc.ruleID)
			if err != nil {
				t.Fatalf("evaluate fixture: %v", err)
			}
			if !strings.Contains(strings.Join(violations, "\n"), "rule="+string(tc.ruleID)) {
				t.Fatalf("fixture %q did not trigger rule %q: %v", tc.relPath, tc.ruleID, violations)
			}
		})
	}
}

func TestAppAdapterBoundaryFixturesRejectCrossDomainPollution(t *testing.T) {
	t.Parallel()
	const ruleID = BoundaryRuleID("app_adapter_narrow_import_surface")
	cases := []struct {
		name       string
		relPath    string
		importPath string
		packageID  string
	}{
		{name: "cron_to_prompt_store", relPath: "internal/app/storeadapter/cron/adapter.go", importPath: "internal/store/prompt", packageID: "cronadapter"},
		{name: "prompt_to_thread_adapter", relPath: "internal/app/storeadapter/prompt/adapter.go", importPath: "internal/app/storeadapter/thread", packageID: "promptadapter"},
		{name: "domain_module_to_runtime_adapter", relPath: "internal/app/storeadapter/cron/module.go", importPath: "internal/app/runtimeadapter/toolbridge", packageID: "cronadapter"},
	}
	registry := DefaultBackendBoundaryRegistry()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := "package " + tc.packageID + "\n\nimport _ \"github.com/lihah111222333-cloud/super-dolphin-agent/" + tc.importPath + "\"\n"
			root := t.TempDir()
			path := writeBackendBoundaryFixture(t, root, tc.relPath, source)
			violations, err := EvaluateBackendBoundaryFile(root, path, tc.relPath, registry, ruleID)
			if err != nil {
				t.Fatalf("evaluate fixture: %v", err)
			}
			got := strings.Join(violations, "\n")
			if !strings.Contains(got, "rule="+string(ruleID)) || !strings.Contains(got, tc.importPath) {
				t.Fatalf("fixture %q did not report import %q through rule %q: %v", tc.relPath, tc.importPath, ruleID, violations)
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
			source:     "package contract\n\nimport _ \"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/thread\"\n",
			wantPrefix: "internal/contract/leak.go:3:10 imports ",
		},
		{
			name:       "import_block",
			source:     "package contract\n\nimport (\n    _ \"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/thread\"\n)\n",
			wantPrefix: "internal/contract/leak.go:4:7 imports ",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			relPath := "internal/contract/leak.go"
			root := t.TempDir()
			path := writeBackendBoundaryFixture(t, root, relPath, tc.source)
			violations, err := EvaluateBackendBoundaryFile(root, path, relPath, registry, "contract_reverse_pollution")
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

func TestEvaluateBackendBoundaryFileBindsCanonicalRootPathAndRelativePath(t *testing.T) {
	root := t.TempDir()
	writeCanonicalSQLCGuardFixture(t, root)
	path := writeBackendBoundaryFixtureFile(t, root, "internal/module/sample.go", "package module\n")
	registry := DefaultBackendBoundaryRegistry()
	assertBackendBoundaryFileError(t, root, path, "internal/module/other.go", registry, "does not match canonical")
	outsideRoot := t.TempDir()
	outside := writeBackendBoundaryFixtureFile(t, outsideRoot, "outside.go", "package outside\n")
	assertBackendBoundaryFileError(t, root, outside, "outside.go", registry, "outside repository root")
	symlink := filepath.Join(root, "internal", "module", "link.go")
	if err := os.Symlink(path, symlink); err != nil {
		t.Fatal(err)
	}
	assertBackendBoundaryFileError(t, root, symlink, "internal/module/link.go", registry, "symbolic link")
}

func writeBackendBoundaryFixtureFile(t *testing.T, root, relative, content string) string {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func assertBackendBoundaryFileError(t *testing.T, root, file, relative string, registry BackendBoundaryRegistry, fragment string) {
	t.Helper()
	_, err := EvaluateBackendBoundaryFile(root, file, relative, registry)
	if err == nil || !strings.Contains(err.Error(), fragment) {
		t.Fatalf("boundary file error = %v, want fragment %q", err, fragment)
	}
}

func TestEvaluateBackendBoundaryRejectsSymlinkedProductionEntry(t *testing.T) {
	root := t.TempDir()
	writeCanonicalSQLCGuardFixture(t, root)
	target := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(target, []byte("package outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "internal", "module", "linked.go")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := EvaluateBackendBoundary(root, DefaultBackendBoundaryRegistry(), "contract_reverse_pollution"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("EvaluateBackendBoundary() error = %v, want symlink rejection", err)
	}
}

func TestPkgNoInternalImportsRuleRejectsRepositoryInternals(t *testing.T) {
	t.Parallel()

	relPath := "pkg/logger/leak.go"
	source := "package logger\n\nimport (\n    _ \"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config\"\n    _ \"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/agent-runtime\"\n    _ \"log/slog\"\n)\n"
	root := t.TempDir()
	path := writeBackendBoundaryFixture(t, root, relPath, source)
	violations, err := EvaluateBackendBoundaryFile(root, path, relPath, DefaultBackendBoundaryRegistry(), "pkg_no_internal_imports")
	if err != nil {
		t.Fatalf("evaluate pkg boundary fixture: %v", err)
	}
	if len(violations) != 2 {
		t.Fatalf("violations = %v, want internal and cmd imports only", violations)
	}
	joined := strings.Join(violations, "\n")
	for _, forbidden := range []string{
		"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config",
		"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/agent-runtime",
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
	writeCanonicalSQLCGuardFixture(t, root)
	path := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create backend boundary fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write backend boundary fixture: %v", err)
	}
	return path
}
