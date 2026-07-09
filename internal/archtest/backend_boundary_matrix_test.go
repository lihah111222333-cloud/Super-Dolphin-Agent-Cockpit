package archtest_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

type backendBoundaryRuleKind string

const (
	backendBoundaryDisallowedImports backendBoundaryRuleKind = "disallowed_imports"
	backendBoundaryAllowedImports    backendBoundaryRuleKind = "allowed_internal_imports"
	backendBoundaryModuleSiblings    backendBoundaryRuleKind = "module_sibling_imports"
	backendBoundaryScopedImport      backendBoundaryRuleKind = "scoped_import"
)

type backendBoundaryMatrix struct {
	Owners []backendBoundaryOwner
	Rules  []backendBoundaryRule
}

type backendBoundaryOwner struct {
	ID           string
	FilePatterns []string
	Reason       string
}

type backendBoundaryRule struct {
	ID                       string
	Owner                    string
	Reason                   string
	Kind                     backendBoundaryRuleKind
	FilePatterns             []string
	DisallowedImportPrefixes []string
	AllowedImportPrefixes    []backendBoundaryImportAllowance
	ImportScopeAllowlist     []backendBoundaryFileAllowance
	Exceptions               []backendBoundaryException
	SkipTestFiles            bool
	DependencyPackages       []string
}

type backendBoundaryImportAllowance struct {
	Owner        string
	FilePattern  string
	ImportPrefix string
	Reason       string
}

type backendBoundaryFileAllowance struct {
	Owner       string
	FilePattern string
	Reason      string
}

type backendBoundaryException struct {
	Owner        string
	FilePattern  string
	ImportPrefix string
	Reason       string
	Temporary    bool
	RemoveWhen   string
}

func TestBackendBoundaryMatrix(t *testing.T) {
	matrix := defaultBackendBoundaryMatrix()
	if violations := validateBackendBoundaryMatrix(matrix); len(violations) > 0 {
		t.Fatalf("backend boundary matrix is invalid:\n%s", strings.Join(violations, "\n"))
	}

	root := repoRoot(t)
	files := parseImportFiles(t, root, "internal", "cmd")
	for _, rule := range matrix.Rules {
		t.Run(rule.ID, func(t *testing.T) {
			violations := backendBoundaryRuleViolations(t, root, rule, files, nil)
			failIfViolations(t, violations)
		})
	}
}

func TestBackendBoundaryMatrixRejectsUnauditedAllowlist(t *testing.T) {
	matrix := defaultBackendBoundaryMatrix()
	matrix.Rules[2].AllowedImportPrefixes = append(matrix.Rules[2].AllowedImportPrefixes, backendBoundaryImportAllowance{
		Owner:        "mcp_sidecar_boundary",
		ImportPrefix: "internal/app",
		Reason:       "synthetic unaudited allowlist fixture",
	})

	violations := strings.Join(validateBackendBoundaryMatrix(matrix), "\n")
	if !strings.Contains(violations, "file_pattern is empty") {
		t.Fatalf("validateBackendBoundaryMatrix() did not reject missing file pattern:\n%s", violations)
	}
}

func TestBackendBoundaryMatrixRejectsGenericStatefulSidecarAllowlist(t *testing.T) {
	matrix := defaultBackendBoundaryMatrix()
	matrix.Rules[2].AllowedImportPrefixes = append(matrix.Rules[2].AllowedImportPrefixes, backendBoundaryImportAllowance{
		Owner:        "mcp_sidecar_boundary",
		FilePattern:  "cmd/mcp-lsp/**/*.go",
		ImportPrefix: "internal/platform/db",
		Reason:       "SQLite lifecycle primitives shared by sidecars",
	})

	violations := strings.Join(validateBackendBoundaryMatrix(matrix), "\n")
	if !strings.Contains(violations, "stateful sidecar allowance must name its sidecar") {
		t.Fatalf("validateBackendBoundaryMatrix() did not reject generic stateful sidecar reason:\n%s", violations)
	}
}

func TestBackendBoundaryMatrixFixturesRejectKnownViolations(t *testing.T) {
	cases := []struct {
		name     string
		ruleID   string
		files    []parsedFile
		deps     map[string][]string
		wantHits []string
	}{
		{
			name:   "contract_reverse_pollution",
			ruleID: "contract_reverse_pollution",
			files: []parsedFile{{
				RelPath: "internal/contract/leak.go",
				Imports: []string{
					internalPrefix("internal/store/thread"),
					internalPrefix("internal/module/thread"),
					internalPrefix("internal/provider/codexapp"),
					modulePath + "/cmd/mcp-orch",
					modulePath + "/frontend-app/src",
				},
			}},
			wantHits: []string{"internal/store/thread", "internal/module/thread", "internal/provider/codexapp", "cmd/mcp-orch", "frontend-app"},
		},
		{
			name:   "module_horizontal_deep_import",
			ruleID: "module_horizontal_deep_import",
			files: []parsedFile{{
				RelPath: "internal/module/thread/service.go",
				Imports: []string{internalPrefix("internal/module/prompt/intent")},
			}},
			wantHits: []string{"internal/module/thread/service.go imports sibling module"},
		},
		{
			name:   "mcp_orch_full_service_dependency",
			ruleID: "mcp_sidecar_narrow_import_surface",
			deps: map[string][]string{
				"cmd/mcp-orch": {internalPrefix("internal/module/thread")},
			},
			wantHits: []string{"cmd/mcp-orch depends on", "internal/module/thread"},
		},
		{
			name:   "mcp_lsp_orch_only_platform_dependency",
			ruleID: "mcp_sidecar_narrow_import_surface",
			deps: map[string][]string{
				"cmd/mcp-lsp": {internalPrefix("internal/platform/notify")},
			},
			wantHits: []string{"cmd/mcp-lsp depends on", "internal/platform/notify"},
		},
		{
			name:   "mcp_ida_lsp_only_platform_dependency",
			ruleID: "mcp_sidecar_narrow_import_surface",
			deps: map[string][]string{
				"cmd/mcp-ida": {internalPrefix("internal/platform/discovery")},
			},
			wantHits: []string{"cmd/mcp-ida depends on", "internal/platform/discovery"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule := mustBackendBoundaryRule(t, tc.ruleID)
			got := strings.Join(backendBoundaryRuleViolations(t, "", rule, tc.files, tc.deps), "\n")
			for _, want := range tc.wantHits {
				if !strings.Contains(got, want) {
					t.Fatalf("backendBoundaryRuleViolations() missing %q in:\n%s", want, got)
				}
			}
		})
	}
}

func defaultBackendBoundaryMatrix() backendBoundaryMatrix {
	return backendBoundaryMatrix{
		Owners: []backendBoundaryOwner{
			{ID: "contract_boundary", FilePatterns: []string{"internal/contract/**/*.go"}, Reason: "contract is the stable DTO and port surface; it must not depend on implementation packages"},
			{ID: "module_boundary", FilePatterns: []string{"internal/module/**/*.go"}, Reason: "business modules own their internals and must communicate through contract/DTO ports"},
			{ID: "mcp_sidecar_boundary", FilePatterns: mcpSidecarFilePatterns(), Reason: "MCP sidecars are standalone entrypoints with only narrow shared internal dependencies"},
			{ID: "toolbridge_boundary", FilePatterns: []string{"internal/platform/toolbridge/**/*.go"}, Reason: "toolbridge is a platform bridge and must not depend upward on module implementations"},
			{ID: "sqlc_boundary", FilePatterns: []string{"internal/**/*.go", "cmd/**/*.go"}, Reason: "sqlc generated code stays behind store/platform persistence boundaries"},
		},
		Rules: []backendBoundaryRule{
			contractReversePollutionRule(),
			moduleHorizontalDeepImportRule(),
			mcpSidecarNarrowImportRule(),
			toolbridgeNoModuleReverseDependencyRule(),
			sqlcStorePlatformBoundaryRule(),
		},
	}
}

func contractReversePollutionRule() backendBoundaryRule {
	return backendBoundaryRule{
		ID:                       "contract_reverse_pollution",
		Owner:                    "contract_boundary",
		Reason:                   "contract may only define stable DTOs and ports, never depend on store/module/provider/cmd/frontend implementation details",
		Kind:                     backendBoundaryDisallowedImports,
		FilePatterns:             []string{"internal/contract/**/*.go"},
		DisallowedImportPrefixes: []string{"internal/store", "internal/module", "internal/provider", "internal/ui", "cmd", "frontend-app"},
		SkipTestFiles:            true,
	}
}

func moduleHorizontalDeepImportRule() backendBoundaryRule {
	return backendBoundaryRule{
		ID:            "module_horizontal_deep_import",
		Owner:         "module_boundary",
		Reason:        "module packages must not import sibling module internals; use contract DTOs or injected ports instead",
		Kind:          backendBoundaryModuleSiblings,
		FilePatterns:  []string{"internal/module/**/*.go"},
		SkipTestFiles: true,
	}
}

func mcpSidecarNarrowImportRule() backendBoundaryRule {
	return backendBoundaryRule{
		ID:                    "mcp_sidecar_narrow_import_surface",
		Owner:                 "mcp_sidecar_boundary",
		Reason:                "cmd/mcp-* can use local sidecar packages plus shared contracts/platform primitives, but not app host, provider, or module services",
		Kind:                  backendBoundaryAllowedImports,
		FilePatterns:          mcpSidecarFilePatterns(),
		AllowedImportPrefixes: mcpSidecarImportAllowances(),
		DisallowedImportPrefixes: []string{
			"internal/app", "internal/module", "internal/provider", "cmd/agent-terminal",
		},
		SkipTestFiles:      true,
		DependencyPackages: []string{"cmd/mcp-orch", "cmd/mcp-lsp", "cmd/mcp-ida"},
	}
}

func toolbridgeNoModuleReverseDependencyRule() backendBoundaryRule {
	return backendBoundaryRule{
		ID:                       "toolbridge_no_module_reverse_dependency",
		Owner:                    "toolbridge_boundary",
		Reason:                   "toolbridge production code must depend on contract ports, not concrete internal/module implementations",
		Kind:                     backendBoundaryDisallowedImports,
		FilePatterns:             []string{"internal/platform/toolbridge/**/*.go"},
		DisallowedImportPrefixes: []string{"internal/module"},
		SkipTestFiles:            true,
	}
}

func sqlcStorePlatformBoundaryRule() backendBoundaryRule {
	return backendBoundaryRule{
		ID:                       "store_sqlc_store_platform_only",
		Owner:                    "sqlc_boundary",
		Reason:                   "store/sqlc generated types must stay inside persistence implementation seams",
		Kind:                     backendBoundaryScopedImport,
		FilePatterns:             []string{"internal/**/*.go", "cmd/**/*.go"},
		DisallowedImportPrefixes: []string{"internal/store/sqlc"},
		ImportScopeAllowlist: []backendBoundaryFileAllowance{
			{Owner: "sqlc_boundary", FilePattern: "internal/store/**/*.go", Reason: "store packages are the canonical anti-corruption wrappers around sqlc"},
			{Owner: "sqlc_boundary", FilePattern: "internal/platform/db/**/*.go", Reason: "platform DB schema verification may inspect sqlc-backed persistence boundaries"},
		},
		SkipTestFiles: true,
	}
}

func mcpSidecarFilePatterns() []string {
	return []string{"cmd/mcp-orch/**/*.go", "cmd/mcp-lsp/**/*.go", "cmd/mcp-ida/**/*.go"}
}

func mcpSidecarImportAllowances() []backendBoundaryImportAllowance {
	common := []struct {
		prefix string
		reason string
	}{
		{"internal/contract", "stable cross-module ports and DTO contracts"},
		{"internal/dto", "pure transport DTOs shared across sidecars"},
		{"internal/mcpserver/common", "shared MCP stdio/bootstrap protocol helpers"},
		{"internal/platform/config", "configuration primitives used by sidecar boot"},
		{"internal/platform/rlimit", "process resource-limit primitives"},
		{"internal/platform/runner", "run-group lifecycle primitives"},
		{"internal/platform/runtimeenv", "runtime environment probes"},
		{"internal/platform/runtimesafe", "runtime safety primitives"},
		{"internal/platform/securefs", "filesystem safety primitives"},
		{"internal/platform/shared", "shared value helpers and embedded prompt constants"},
		{"internal/util", "leaf utility packages without module ownership"},
	}
	var out []backendBoundaryImportAllowance
	for _, pattern := range mcpSidecarFilePatterns() {
		out = appendBackendBoundaryImportAllowances(out, pattern, common)
	}
	out = appendBackendBoundaryImportAllowances(out, "cmd/mcp-orch/**/*.go", []struct {
		prefix string
		reason string
	}{
		{"internal/platform/bus", "orchestration sidecar publishes typed runtime events"},
		{"internal/platform/db", "orchestration sidecar owns task DAG and shared-file SQLite stores"},
		{"internal/platform/discovery", "orchestration sidecar discovers runtime peers and workspaces"},
		{"internal/platform/eventsurface", "orchestration sidecar exposes event-surface adapters"},
		{"internal/platform/metrics", "orchestration sidecar exports runtime and task metrics"},
		{"internal/platform/notify", "orchestration sidecar emits user-visible notifications"},
		{"internal/platform/rpc", "orchestration sidecar uses RPC client-side protocol primitives only; host symbols are guarded separately"},
		{"internal/platform/sharedfilefs", "orchestration sidecar owns shared-file filesystem access"},
		{"internal/platform/sharedfilegitignore", "orchestration sidecar applies shared-file gitignore rules"},
		{"internal/platform/sharedfilepath", "orchestration sidecar normalizes shared-file paths"},
		{"internal/platform/statemachine", "orchestration sidecar owns agent lifecycle state machines"},
	})
	out = appendBackendBoundaryImportAllowances(out, "cmd/mcp-lsp/**/*.go", []struct {
		prefix string
		reason string
	}{
		{"internal/platform/db", "LSP sidecar reuses MCP bootstrap DB lifecycle for sidecar readiness"},
		{"internal/platform/discovery", "LSP sidecar discovers language server workspace capabilities"},
		{"internal/platform/metrics", "LSP sidecar exposes language-server runtime metrics"},
	})
	out = appendBackendBoundaryImportAllowances(out, "cmd/mcp-ida/**/*.go", []struct {
		prefix string
		reason string
	}{
		{"internal/platform/db", "IDA sidecar reuses MCP bootstrap DB lifecycle for sidecar readiness"},
		{"internal/platform/metrics", "IDA sidecar exposes analysis runtime metrics"},
	})
	return out
}

func appendBackendBoundaryImportAllowances(
	out []backendBoundaryImportAllowance,
	pattern string,
	prefixes []struct {
		prefix string
		reason string
	},
) []backendBoundaryImportAllowance {
	for _, prefix := range prefixes {
		out = append(out, backendBoundaryImportAllowance{
			Owner:        "mcp_sidecar_boundary",
			FilePattern:  pattern,
			ImportPrefix: prefix.prefix,
			Reason:       prefix.reason,
		})
	}
	return out
}

func mustBackendBoundaryRule(t *testing.T, id string) backendBoundaryRule {
	t.Helper()
	for _, rule := range defaultBackendBoundaryMatrix().Rules {
		if rule.ID == id {
			return rule
		}
	}
	t.Fatalf("backend boundary rule %q is not registered", id)
	return backendBoundaryRule{}
}

func backendBoundaryRuleViolations(t *testing.T, root string, rule backendBoundaryRule, files []parsedFile, deps map[string][]string) []string {
	t.Helper()
	var violations []string
	for _, file := range files {
		if backendBoundaryRuleSkipsFile(rule, file.RelPath) {
			continue
		}
		violations = append(violations, backendBoundaryFileViolations(rule, file)...)
	}
	violations = append(violations, backendBoundaryDependencyViolations(t, root, rule, deps)...)
	return violations
}

func backendBoundaryFileViolations(rule backendBoundaryRule, file parsedFile) []string {
	switch rule.Kind {
	case backendBoundaryDisallowedImports:
		return backendBoundaryDisallowedImportViolations(rule, file)
	case backendBoundaryModuleSiblings:
		return backendBoundaryModuleSiblingViolations(rule, file)
	case backendBoundaryAllowedImports:
		return backendBoundaryAllowedImportViolations(rule, file)
	case backendBoundaryScopedImport:
		return backendBoundaryScopedImportViolations(rule, file)
	default:
		return []string{fmt.Sprintf("%s has unknown backend boundary rule kind %q", rule.ID, rule.Kind)}
	}
}

func backendBoundaryRuleSkipsFile(rule backendBoundaryRule, relPath string) bool {
	if rule.SkipTestFiles && strings.HasSuffix(relPath, "_test.go") {
		return true
	}
	return !backendBoundaryMatchesAnyPattern(rule.FilePatterns, relPath)
}

func backendBoundaryDisallowedImportViolations(rule backendBoundaryRule, file parsedFile) []string {
	var violations []string
	for _, imp := range file.Imports {
		if backendBoundaryImportDisallowed(rule, file.RelPath, imp) {
			violations = append(violations, backendBoundaryImportViolation(rule, file.RelPath, imp))
		}
	}
	return violations
}

func backendBoundaryModuleSiblingViolations(rule backendBoundaryRule, file parsedFile) []string {
	owner, ok := moduleOwnerForImportCheck(file.RelPath)
	if !ok {
		return nil
	}
	var violations []string
	for _, violation := range moduleSiblingImportViolations(file, owner) {
		violations = append(violations, fmt.Sprintf("%s (rule=%s owner=%s)", violation, rule.ID, rule.Owner))
	}
	return violations
}

func backendBoundaryAllowedImportViolations(rule backendBoundaryRule, file parsedFile) []string {
	var violations []string
	for _, imp := range file.Imports {
		if !backendBoundaryInternalOrCmdImport(imp) || backendBoundaryOwnCmdImport(file.RelPath, imp) {
			continue
		}
		if backendBoundaryImportDisallowed(rule, file.RelPath, imp) || !backendBoundaryImportAllowed(rule, file.RelPath, imp) {
			violations = append(violations, backendBoundaryImportViolation(rule, file.RelPath, imp))
		}
	}
	return violations
}

func backendBoundaryScopedImportViolations(rule backendBoundaryRule, file parsedFile) []string {
	var violations []string
	for _, imp := range file.Imports {
		if !backendBoundaryImportDisallowed(rule, file.RelPath, imp) {
			continue
		}
		if backendBoundaryFileAllowedForImport(rule, file.RelPath) {
			continue
		}
		violations = append(violations, backendBoundaryImportViolation(rule, file.RelPath, imp))
	}
	return violations
}

func backendBoundaryDependencyViolations(t *testing.T, root string, rule backendBoundaryRule, deps map[string][]string) []string {
	t.Helper()
	if rule.Kind != backendBoundaryAllowedImports {
		return nil
	}
	var violations []string
	for _, relPkg := range rule.DependencyPackages {
		for _, dep := range backendBoundaryDepsForPackage(t, root, relPkg, deps) {
			if !backendBoundaryInternalOrCmdImport(dep) || backendBoundaryDependencyOwnPackage(dep, relPkg) {
				continue
			}
			sourceRel := filepath.ToSlash(filepath.Join(relPkg, "main.go"))
			if backendBoundaryImportDisallowed(rule, sourceRel, dep) || !backendBoundaryImportAllowed(rule, sourceRel, dep) {
				violations = append(violations, fmt.Sprintf("%s depends on %s outside backend boundary matrix (rule=%s owner=%s)", relPkg, dep, rule.ID, rule.Owner))
			}
		}
	}
	return violations
}

func backendBoundaryDepsForPackage(t *testing.T, root, relPkg string, deps map[string][]string) []string {
	t.Helper()
	if deps != nil {
		return deps[relPkg]
	}
	if root == "" || !dirExists(root, relPkg) {
		return nil
	}
	return goListDeps(t, root, relPkg)
}

func backendBoundaryImportDisallowed(rule backendBoundaryRule, relPath, imp string) bool {
	for _, prefix := range rule.DisallowedImportPrefixes {
		if backendBoundaryImportMatchesPrefix(imp, prefix) && !backendBoundaryExceptionAllows(rule, relPath, imp) {
			return true
		}
	}
	return false
}

func backendBoundaryImportAllowed(rule backendBoundaryRule, relPath, imp string) bool {
	for _, allowed := range rule.AllowedImportPrefixes {
		if !backendBoundaryPatternMatches(allowed.FilePattern, relPath) {
			continue
		}
		if backendBoundaryImportMatchesPrefix(imp, allowed.ImportPrefix) {
			return true
		}
	}
	return false
}

func backendBoundaryFileAllowedForImport(rule backendBoundaryRule, relPath string) bool {
	for _, allowed := range rule.ImportScopeAllowlist {
		if backendBoundaryPatternMatches(allowed.FilePattern, relPath) {
			return true
		}
	}
	return false
}

func backendBoundaryExceptionAllows(rule backendBoundaryRule, relPath, imp string) bool {
	for _, exception := range rule.Exceptions {
		if !backendBoundaryPatternMatches(exception.FilePattern, relPath) {
			continue
		}
		if backendBoundaryImportMatchesPrefix(imp, exception.ImportPrefix) {
			return true
		}
	}
	return false
}

func backendBoundaryImportMatchesPrefix(imp, prefix string) bool {
	if prefix == "frontend-app" {
		return strings.Contains(imp, "/frontend-app") || strings.HasPrefix(imp, "frontend-app")
	}
	if prefix == "cmd" || strings.HasPrefix(prefix, "cmd/") {
		return importPathMatchesPrefix(imp, modulePath+"/"+strings.Trim(prefix, "/"))
	}
	return importPathMatchesPrefix(imp, boundaryImportPrefix(prefix))
}

func backendBoundaryInternalOrCmdImport(imp string) bool {
	return strings.HasPrefix(imp, modulePath+"/internal/") || strings.HasPrefix(imp, modulePath+"/cmd/")
}

func backendBoundaryOwnCmdImport(relPath, imp string) bool {
	for _, prefix := range []string{"cmd/mcp-orch", "cmd/mcp-lsp", "cmd/mcp-ida"} {
		if strings.HasPrefix(relPath, prefix+"/") && backendBoundaryDependencyOwnPackage(imp, prefix) {
			return true
		}
	}
	return false
}

func backendBoundaryDependencyOwnPackage(dep, relPkg string) bool {
	full := modulePath + "/" + relPkg
	return dep == full || strings.HasPrefix(dep, full+"/")
}

func backendBoundaryImportViolation(rule backendBoundaryRule, relPath, imp string) string {
	return fmt.Sprintf("%s imports %s (rule=%s owner=%s reason=%s)", relPath, imp, rule.ID, rule.Owner, rule.Reason)
}

func backendBoundaryMatchesAnyPattern(patterns []string, relPath string) bool {
	for _, pattern := range patterns {
		if backendBoundaryPatternMatches(pattern, relPath) {
			return true
		}
	}
	return false
}

func backendBoundaryPatternMatches(pattern, relPath string) bool {
	pattern = filepath.ToSlash(strings.TrimPrefix(pattern, "./"))
	relPath = filepath.ToSlash(strings.TrimPrefix(relPath, "./"))
	switch {
	case strings.HasSuffix(pattern, "/**/*.go"):
		base := strings.TrimSuffix(pattern, "/**/*.go")
		return strings.HasPrefix(relPath, base+"/") && strings.HasSuffix(relPath, ".go")
	case strings.HasSuffix(pattern, "/**"):
		base := strings.TrimSuffix(pattern, "/**")
		return relPath == base || strings.HasPrefix(relPath, base+"/")
	default:
		return relPath == pattern
	}
}

func validateBackendBoundaryMatrix(matrix backendBoundaryMatrix) []string {
	owners, violations := validateBackendBoundaryOwners(matrix.Owners)
	violations = append(violations, validateBackendBoundaryRules(matrix.Rules, owners)...)
	return violations
}

func validateBackendBoundaryOwners(matrixOwners []backendBoundaryOwner) (map[string]bool, []string) {
	owners := map[string]bool{}
	var violations []string
	for i, owner := range matrixOwners {
		label := fmt.Sprintf("owner[%d]", i)
		violations = append(violations, validateBackendBoundaryOwner(label, owner, owners)...)
		if strings.TrimSpace(owner.ID) != "" {
			owners[owner.ID] = true
		}
	}
	return owners, violations
}

func validateBackendBoundaryOwner(label string, owner backendBoundaryOwner, owners map[string]bool) []string {
	var violations []string
	if strings.TrimSpace(owner.ID) == "" {
		violations = append(violations, label+" id is empty")
	} else if owners[owner.ID] {
		violations = append(violations, label+" duplicate owner "+owner.ID)
	}
	if len(owner.FilePatterns) == 0 {
		violations = append(violations, label+" file_patterns are empty")
	}
	if strings.TrimSpace(owner.Reason) == "" {
		violations = append(violations, label+" reason is empty")
	}
	return append(violations, validateBackendBoundaryPatterns(label+".file_patterns", owner.FilePatterns)...)
}

func validateBackendBoundaryRules(rules []backendBoundaryRule, owners map[string]bool) []string {
	ruleIDs := map[string]bool{}
	var violations []string
	for i, rule := range rules {
		label := fmt.Sprintf("rule[%d]", i)
		violations = append(violations, validateBackendBoundaryRule(label, rule, owners, ruleIDs)...)
		if strings.TrimSpace(rule.ID) != "" {
			ruleIDs[rule.ID] = true
		}
	}
	return violations
}

func validateBackendBoundaryRule(label string, rule backendBoundaryRule, owners, ruleIDs map[string]bool) []string {
	var violations []string
	violations = append(violations, validateBackendBoundaryRuleHeader(label, rule, owners, ruleIDs)...)
	violations = append(violations, validateBackendBoundaryPatterns(label+".file_patterns", rule.FilePatterns)...)
	violations = append(violations, validateBackendBoundaryImportAllowances(label+".allowed", rule.AllowedImportPrefixes, owners)...)
	violations = append(violations, validateBackendBoundaryFileAllowances(label+".scope_allowlist", rule.ImportScopeAllowlist, owners)...)
	violations = append(violations, validateBackendBoundaryExceptions(label+".exception", rule.Exceptions, owners)...)
	return violations
}

func validateBackendBoundaryRuleHeader(label string, rule backendBoundaryRule, owners, ruleIDs map[string]bool) []string {
	var violations []string
	if strings.TrimSpace(rule.ID) == "" {
		violations = append(violations, label+" id is empty")
	} else if ruleIDs[rule.ID] {
		violations = append(violations, label+" duplicate rule "+rule.ID)
	}
	if strings.TrimSpace(rule.Owner) == "" {
		violations = append(violations, label+" owner is empty")
	} else if !owners[rule.Owner] {
		violations = append(violations, label+" unknown owner "+rule.Owner)
	}
	if strings.TrimSpace(rule.Reason) == "" {
		violations = append(violations, label+" reason is empty")
	}
	if strings.TrimSpace(string(rule.Kind)) == "" {
		violations = append(violations, label+" kind is empty")
	}
	if len(rule.FilePatterns) == 0 {
		violations = append(violations, label+" file_patterns are empty")
	}
	return violations
}

func validateBackendBoundaryPatterns(label string, patterns []string) []string {
	var violations []string
	for i, pattern := range patterns {
		if strings.TrimSpace(pattern) == "" {
			violations = append(violations, fmt.Sprintf("%s[%d] is empty", label, i))
		}
	}
	return violations
}

func validateBackendBoundaryImportAllowances(label string, allowances []backendBoundaryImportAllowance, owners map[string]bool) []string {
	var violations []string
	for i, allowance := range allowances {
		item := fmt.Sprintf("%s[%d]", label, i)
		violations = append(violations, validateBackendBoundaryOwnedAuditFields(item, allowance.Owner, allowance.FilePattern, allowance.Reason, owners)...)
		if strings.TrimSpace(allowance.ImportPrefix) == "" {
			violations = append(violations, item+" import_prefix is empty")
		}
		if backendBoundaryStatefulSidecarAllowanceIsGeneric(allowance) {
			violations = append(violations, item+" stateful sidecar allowance must name its sidecar")
		}
	}
	return violations
}

func backendBoundaryStatefulSidecarAllowanceIsGeneric(allowance backendBoundaryImportAllowance) bool {
	if !backendBoundaryAllowancePrefixMatches(allowance.ImportPrefix, "internal/platform/db") &&
		!backendBoundaryAllowancePrefixMatches(allowance.ImportPrefix, "internal/platform/metrics") {
		return false
	}
	if !strings.HasPrefix(allowance.FilePattern, "cmd/mcp-") {
		return false
	}
	sidecar := strings.TrimSuffix(strings.TrimPrefix(allowance.FilePattern, "cmd/mcp-"), "/**/*.go")
	return !strings.Contains(strings.ToLower(allowance.Reason), backendBoundarySidecarReasonName(sidecar)+" sidecar")
}

func backendBoundaryAllowancePrefixMatches(got, want string) bool {
	return got == want || strings.HasPrefix(got, want+"/")
}

func backendBoundarySidecarReasonName(sidecar string) string {
	if sidecar == "orch" {
		return "orchestration"
	}
	return sidecar
}

func validateBackendBoundaryFileAllowances(label string, allowances []backendBoundaryFileAllowance, owners map[string]bool) []string {
	var violations []string
	for i, allowance := range allowances {
		item := fmt.Sprintf("%s[%d]", label, i)
		violations = append(violations, validateBackendBoundaryOwnedAuditFields(item, allowance.Owner, allowance.FilePattern, allowance.Reason, owners)...)
	}
	return violations
}

func validateBackendBoundaryExceptions(label string, exceptions []backendBoundaryException, owners map[string]bool) []string {
	var violations []string
	for i, exception := range exceptions {
		item := fmt.Sprintf("%s[%d]", label, i)
		violations = append(violations, validateBackendBoundaryOwnedAuditFields(item, exception.Owner, exception.FilePattern, exception.Reason, owners)...)
		if strings.TrimSpace(exception.ImportPrefix) == "" {
			violations = append(violations, item+" import_prefix is empty")
		}
		if exception.Temporary && strings.TrimSpace(exception.RemoveWhen) == "" {
			violations = append(violations, item+" temporary exception missing remove_when")
		}
	}
	return violations
}

func validateBackendBoundaryOwnedAuditFields(label, owner, filePattern, reason string, owners map[string]bool) []string {
	var violations []string
	if strings.TrimSpace(owner) == "" {
		violations = append(violations, label+" owner is empty")
	} else if !owners[owner] {
		violations = append(violations, label+" unknown owner "+owner)
	}
	if strings.TrimSpace(filePattern) == "" {
		violations = append(violations, label+" file_pattern is empty")
	}
	if strings.TrimSpace(reason) == "" {
		violations = append(violations, label+" reason is empty")
	}
	return violations
}
