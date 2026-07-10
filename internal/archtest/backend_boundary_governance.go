package archtest

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

func defaultBackendBoundaryGuards() []BackendBoundaryGuard {
	return []BackendBoundaryGuard{
		{ID: "backend_surface_governance", File: "internal/archtest/backend_boundary_governance_test.go", TestNames: []string{"TestValidateDefaultBackendBoundaryGovernance"}, Reason: "the governance guard fails when a backend top-level Go surface is missing or stale"},
		{ID: "backend_boundary_single_source", File: "internal/archtest/backend_boundary_single_source_test.go", TestNames: []string{"TestBackendBoundaryRuleFactsHaveOneSource"}, Reason: "canonical backend boundary facts must not be duplicated by procedural evaluators"},
		{ID: "dependency_direction", File: "internal/archtest/dependency_direction_test.go", TestNames: []string{"TestDependencyDirection"}, Reason: "dependency direction tests protect typed backend layer relationships"},
		{ID: "fx_graph", File: "internal/archtest/fx_graph_test.go", TestNames: []string{"TestFxValidateApp"}, Reason: "the desktop composition root must retain a valid Fx graph"},
		{ID: "pkg_public_boundary", File: "internal/archtest/backend_boundary_guard_coverage_test.go", TestNames: []string{"TestPkgNoInternalImportsRuleRejectsRepositoryInternals"}, Reason: "public pkg libraries must reject both repository internals and command entrypoints"},
		{ID: "ui_wails_boundary", File: "internal/archtest/ui_wails_guard_test.go", TestNames: []string{"TestUIWailsNoDirectUIStateImport", "TestUIWailsActiveAgentPredicateFromContract"}, Reason: "Wails UI bindings consume contract-facing state instead of module implementations"},
	}
}

// defaultBackendBoundarySurfaces 显式登记当前后端一级目录的 canonical rule 与专项 guard 归属。
func defaultBackendBoundarySurfaces() []BackendBoundarySurface {
	return []BackendBoundarySurface{
		backendBoundarySurface("cmd/agent-runtime", "agent runtime process assembly", []BoundaryRuleID{"fx_assembly_scope"}, nil),
		backendBoundarySurface("cmd/agent-terminal", "agent terminal process assembly", []BoundaryRuleID{"fx_assembly_scope"}, nil),
		backendBoundarySurface("cmd/mcp-ida", "IDA MCP sidecar boundary", []BoundaryRuleID{"mcp_sidecar_narrow_import_surface", "fx_assembly_scope", "mcpserver_ida_family"}, nil),
		backendBoundarySurface("cmd/mcp-lsp", "LSP MCP sidecar boundary", []BoundaryRuleID{"mcp_sidecar_narrow_import_surface", "fx_assembly_scope"}, nil),
		backendBoundarySurface("cmd/mcp-orch", "orchestration MCP sidecar boundary", []BoundaryRuleID{"mcp_sidecar_narrow_import_surface", "fx_assembly_scope", "mcpserver_orch_family"}, nil),
		backendBoundarySurface("cmd/super-dolphin-release-manifest", "release manifest command assembly", []BoundaryRuleID{"fx_assembly_scope"}, nil),
		backendBoundarySurface("cmd/super-dolphin-updater", "updater command assembly", []BoundaryRuleID{"fx_assembly_scope"}, nil),
		backendBoundarySurface("internal/app", "desktop composition root", []BoundaryRuleID{"fx_assembly_scope"}, []BoundaryGuardID{"fx_graph"}),
		backendBoundarySurface("internal/archtest", "architecture governance implementation", []BoundaryRuleID{"fx_assembly_scope"}, []BoundaryGuardID{"backend_boundary_single_source"}),
		backendBoundarySurface("internal/contract", "stable DTO and port contracts", []BoundaryRuleID{"contract_reverse_pollution"}, nil),
		backendBoundarySurface("internal/devtools", "backend developer tooling", []BoundaryRuleID{"fx_assembly_scope"}, nil),
		backendBoundarySurface("internal/dto", "transport-neutral data transfer objects", []BoundaryRuleID{"fx_assembly_scope"}, nil),
		backendBoundarySurface("internal/e2e", "backend end-to-end test surface", nil, []BoundaryGuardID{"backend_surface_governance"}),
		backendBoundarySurface("internal/guards", "repository-level test guard surface", nil, []BoundaryGuardID{"backend_surface_governance"}),
		backendBoundarySurface("internal/mcpserver", "shared MCP server implementations", []BoundaryRuleID{"fx_assembly_scope"}, []BoundaryGuardID{"dependency_direction"}),
		backendBoundarySurface("internal/module", "business module ownership", []BoundaryRuleID{"module_horizontal_deep_import", "module_no_direct_db_imports", "fx_assembly_scope"}, nil),
		backendBoundarySurface("internal/platform", "infrastructure runtime layer", []BoundaryRuleID{"platform_no_module", "platform_no_store", "fx_assembly_scope"}, nil),
		backendBoundarySurface("internal/provider", "provider adapter runtime", []BoundaryRuleID{"provider_no_store", "provider_no_platform_db", "fx_assembly_scope"}, nil),
		backendBoundarySurface("internal/store", "persistence anti-corruption layer", []BoundaryRuleID{"store_dependency_surface", "fx_assembly_scope"}, nil),
		backendBoundarySurface("internal/testutil", "shared backend test support", []BoundaryRuleID{"fx_assembly_scope"}, nil),
		backendBoundarySurface("internal/ui", "Wails backend binding layer", []BoundaryRuleID{"fx_assembly_scope"}, []BoundaryGuardID{"ui_wails_boundary"}),
		backendBoundarySurface("internal/util", "shared backend utilities", []BoundaryRuleID{"fx_assembly_scope"}, nil),
		backendBoundarySurface("pkg/dagmetrics", "public DAG metrics library", []BoundaryRuleID{"pkg_no_internal_imports"}, []BoundaryGuardID{"pkg_public_boundary"}),
		backendBoundarySurface("pkg/dreammetrics", "public dream metrics library", []BoundaryRuleID{"pkg_no_internal_imports"}, []BoundaryGuardID{"pkg_public_boundary"}),
		backendBoundarySurface("pkg/logger", "public logging library", []BoundaryRuleID{"pkg_no_internal_imports"}, []BoundaryGuardID{"pkg_public_boundary"}),
		backendBoundarySurface("pkg/skillmetrics", "public skill metrics library", []BoundaryRuleID{"pkg_no_internal_imports"}, []BoundaryGuardID{"pkg_public_boundary"}),
	}
}

func backendBoundarySurface(path, reason string, rules []BoundaryRuleID, guards []BoundaryGuardID) BackendBoundarySurface {
	return BackendBoundarySurface{Path: path, RuleIDs: rules, GuardIDs: guards, Reason: reason}
}

// ValidateBackendBoundaryGovernance 校验后端顶层目录、canonical rule 与真实专项测试入口的一致性。
func ValidateBackendBoundaryGovernance(root string, registry BackendBoundaryRegistry) []string {
	violations := ValidateBackendBoundaryRegistry(registry)
	actual, err := discoverBackendBoundarySurfaces(root)
	if err != nil {
		violations = append(violations, fmt.Sprintf("discover backend surfaces: %v", err))
		sort.Strings(violations)
		return violations
	}
	violations = append(violations, validateBackendSurfaceInventory(actual, registry.Surfaces)...)
	violations = append(violations, validateBackendSurfaceRuleCoverage(actual, registry)...)
	violations = append(violations, validateBackendBoundaryGuardFiles(root, registry.Guards)...)
	sort.Strings(violations)
	return violations
}

// validateBackendBoundaryGovernanceRegistry 检查不依赖文件系统的 guard 与 surface 引用不变量。
func validateBackendBoundaryGovernanceRegistry(registry BackendBoundaryRegistry) []string {
	rules := make(map[BoundaryRuleID]bool, len(registry.Rules))
	for _, rule := range registry.Rules {
		rules[rule.ID] = true
	}
	guards, violations := validateBackendBoundaryGuardDescriptors(registry.Guards)
	surfaceViolations, references := validateBackendBoundarySurfaceDescriptors(registry.Surfaces, rules, guards)
	violations = append(violations, surfaceViolations...)
	for id := range guards {
		if references[id] == 0 {
			violations = append(violations, fmt.Sprintf("guard %q is not referenced by any backend surface", id))
		}
	}
	return violations
}

// validateBackendBoundaryGuardDescriptors 校验 guard ID 唯一性和静态字段约束。
func validateBackendBoundaryGuardDescriptors(items []BackendBoundaryGuard) (map[BoundaryGuardID]bool, []string) {
	guards := make(map[BoundaryGuardID]bool, len(items))
	var violations []string
	for i, guard := range items {
		label := fmt.Sprintf("guard[%d]", i)
		if strings.TrimSpace(string(guard.ID)) == "" {
			violations = append(violations, label+" id is empty")
		} else if guards[guard.ID] {
			violations = append(violations, fmt.Sprintf("%s duplicate guard %q", label, guard.ID))
		} else {
			guards[guard.ID] = true
		}
		violations = append(violations, validateBackendBoundaryGuardFields(label, guard)...)
	}
	return guards, violations
}

// validateBackendBoundaryGuardFields 拒绝空原因、非规范文件和不可发现的测试名。
func validateBackendBoundaryGuardFields(label string, guard BackendBoundaryGuard) []string {
	var violations []string
	if strings.TrimSpace(guard.Reason) == "" {
		violations = append(violations, label+" reason is empty")
	}
	if !isCanonicalBackendBoundaryGuardFile(guard.File) {
		violations = append(violations, fmt.Sprintf("%s file %q must be a canonical internal/archtest/*_test.go path", label, guard.File))
	}
	if len(guard.TestNames) == 0 {
		violations = append(violations, label+" test_names is empty")
	}
	seen := make(map[string]bool, len(guard.TestNames))
	for _, name := range guard.TestNames {
		if !isGoTestName(name) {
			violations = append(violations, fmt.Sprintf("%s test name %q is not discoverable by Go testing", label, name))
		} else if seen[name] {
			violations = append(violations, fmt.Sprintf("%s duplicate test name %q", label, name))
		}
		seen[name] = true
	}
	return violations
}

// validateBackendBoundarySurfaceDescriptors 拒绝空机制、重复目录以及未知 rule/guard 引用。
func validateBackendBoundarySurfaceDescriptors(items []BackendBoundarySurface, rules map[BoundaryRuleID]bool, guards map[BoundaryGuardID]bool) ([]string, map[BoundaryGuardID]int) {
	seen := make(map[string]bool, len(items))
	references := make(map[BoundaryGuardID]int, len(guards))
	var violations []string
	for i, surface := range items {
		label := fmt.Sprintf("surface[%d]", i)
		if !isCanonicalBackendBoundarySurface(surface.Path) {
			violations = append(violations, fmt.Sprintf("%s path %q must be cmd/<name>, internal/<name>, or pkg/<name>", label, surface.Path))
		} else if seen[surface.Path] {
			violations = append(violations, fmt.Sprintf("%s duplicate backend surface %q", label, surface.Path))
		}
		seen[surface.Path] = true
		violations = append(violations, validateBackendBoundarySurfaceFields(label, surface, rules, guards, references)...)
	}
	return violations, references
}

func validateBackendBoundarySurfaceFields(label string, surface BackendBoundarySurface, rules map[BoundaryRuleID]bool, guards map[BoundaryGuardID]bool, references map[BoundaryGuardID]int) []string {
	var violations []string
	if strings.TrimSpace(surface.Reason) == "" {
		violations = append(violations, label+" reason is empty")
	}
	if len(surface.RuleIDs) == 0 && len(surface.GuardIDs) == 0 {
		violations = append(violations, fmt.Sprintf("%s %q has no canonical rules or specialized guards", label, surface.Path))
	}
	violations = append(violations, validateBackendBoundarySurfaceRules(label, surface.RuleIDs, rules)...)
	violations = append(violations, validateBackendBoundarySurfaceGuards(label, surface.GuardIDs, guards, references)...)
	return violations
}

// validateBackendBoundarySurfaceRules 拒绝 surface 中未知或重复的 canonical rule。
func validateBackendBoundarySurfaceRules(label string, ids []BoundaryRuleID, known map[BoundaryRuleID]bool) []string {
	seen := make(map[BoundaryRuleID]bool, len(ids))
	var violations []string
	for _, id := range ids {
		if !known[id] {
			violations = append(violations, fmt.Sprintf("%s unknown rule %q", label, id))
		} else if seen[id] {
			violations = append(violations, fmt.Sprintf("%s duplicate rule %q", label, id))
		}
		seen[id] = true
	}
	return violations
}

// validateBackendBoundarySurfaceGuards 拒绝未知或重复 guard 并累计真实引用。
func validateBackendBoundarySurfaceGuards(label string, ids []BoundaryGuardID, known map[BoundaryGuardID]bool, references map[BoundaryGuardID]int) []string {
	seen := make(map[BoundaryGuardID]bool, len(ids))
	var violations []string
	for _, id := range ids {
		if !known[id] {
			violations = append(violations, fmt.Sprintf("%s unknown guard %q", label, id))
		} else if seen[id] {
			violations = append(violations, fmt.Sprintf("%s duplicate guard %q", label, id))
		}
		seen[id] = true
		references[id]++
	}
	return violations
}

func isCanonicalBackendBoundaryGuardFile(path string) bool {
	return !strings.Contains(path, "\\") && path == filepath.ToSlash(filepath.Clean(path)) &&
		strings.HasPrefix(path, "internal/archtest/") && strings.HasSuffix(path, "_test.go")
}

// isCanonicalBackendBoundarySurface 只接受三个后端根下恰好一级的规范相对目录。
func isCanonicalBackendBoundarySurface(path string) bool {
	if path != filepath.ToSlash(filepath.Clean(path)) || strings.Contains(path, "\\") {
		return false
	}
	parts := strings.Split(path, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return false
	}
	return parts[0] == "cmd" || parts[0] == "internal" || parts[0] == "pkg"
}

// discoverBackendBoundarySurfaces 返回每个含 Go 源码的后端一级目录及其相对文件。
func discoverBackendBoundarySurfaces(root string) (map[string][]string, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("repository root is empty")
	}
	if info, err := os.Stat(root); err != nil {
		return nil, err
	} else if !info.IsDir() {
		return nil, fmt.Errorf("repository root %s is not a directory", root)
	}
	result := make(map[string][]string)
	for _, family := range []string{"cmd", "internal", "pkg"} {
		if err := discoverBackendBoundaryFamily(root, family, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// discoverBackendBoundaryFamily 发现一个后端根下递归包含 Go 文件的一级目录。
func discoverBackendBoundaryFamily(root, family string, result map[string][]string) error {
	entries, err := os.ReadDir(filepath.Join(root, family))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", family, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		surface := family + "/" + entry.Name()
		files, err := collectBackendSurfaceGoFiles(root, surface)
		if err != nil {
			return err
		}
		if len(files) > 0 {
			result[surface] = files
		}
	}
	return nil
}

// collectBackendSurfaceGoFiles 遵循统一跳过目录配置收集 surface 内的全部 Go 源码。
func collectBackendSurfaceGoFiles(root, surface string) ([]string, error) {
	skip := DefaultSkipDirs()
	var files []string
	err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(surface)), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && path != filepath.Join(root, filepath.FromSlash(surface)) && skip[entry.Name()] {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(files)
	return files, err
}

// validateBackendSurfaceInventory 对实际目录和 registry 声明执行双向精确比较。
func validateBackendSurfaceInventory(actual map[string][]string, declared []BackendBoundarySurface) []string {
	registered := make(map[string]bool, len(declared))
	for _, surface := range declared {
		registered[surface.Path] = true
	}
	var violations []string
	for surface := range actual {
		if !registered[surface] {
			violations = append(violations, fmt.Sprintf("unregistered backend surface %q", surface))
		}
	}
	for surface := range registered {
		if len(actual[surface]) == 0 {
			violations = append(violations, fmt.Sprintf("registered backend surface %q is missing or contains no Go source", surface))
		}
	}
	return violations
}

// validateBackendSurfaceRuleCoverage 证明 surface 引用的每条 rule 至少匹配一个实际适用文件。
func validateBackendSurfaceRuleCoverage(actual map[string][]string, registry BackendBoundaryRegistry) []string {
	rules := make(map[BoundaryRuleID]BackendBoundaryRule, len(registry.Rules))
	for _, rule := range registry.Rules {
		rules[rule.ID] = rule
	}
	var violations []string
	for _, surface := range registry.Surfaces {
		for _, id := range surface.RuleIDs {
			rule, ok := rules[id]
			if !ok {
				continue
			}
			if !backendBoundaryRuleMatchesSurfaceFiles(rule, actual[surface.Path]) {
				violations = append(violations, fmt.Sprintf("backend surface %q rule %q matches no applicable Go file", surface.Path, id))
				continue
			}
			if !backendBoundaryRuleEnforcesSurfaceFiles(rule, actual[surface.Path]) {
				violations = append(violations, fmt.Sprintf("backend surface %q rule %q has no enforcing policy for an applicable Go file", surface.Path, id))
			}
		}
	}
	return violations
}

// backendBoundaryRuleEnforcesSurfaceFiles 区分声明范围命中与策略实际生效，拒绝空壳 rule 引用。
func backendBoundaryRuleEnforcesSurfaceFiles(rule BackendBoundaryRule, files []string) bool {
	for _, rel := range files {
		if rule.SkipTestFiles && strings.HasSuffix(rel, "_test.go") {
			continue
		}
		if !matchesAnyBackendBoundaryPattern(rule.FilePatterns, rel) {
			continue
		}
		switch rule.Kind {
		case BoundaryRuleDenyImports, BoundaryRuleScopedImport:
			if backendBoundaryImportPolicyMatchesFile(rule.Deny, rel) {
				return true
			}
		default:
			return true
		}
	}
	return false
}

func backendBoundaryImportPolicyMatchesFile(policies []BoundaryImportPolicy, rel string) bool {
	for _, policy := range policies {
		if matchesBackendBoundaryPattern(policy.FilePattern, rel) {
			return true
		}
	}
	return false
}

func backendBoundaryRuleMatchesSurfaceFiles(rule BackendBoundaryRule, files []string) bool {
	for _, rel := range files {
		if rule.SkipTestFiles && strings.HasSuffix(rel, "_test.go") {
			continue
		}
		if matchesAnyBackendBoundaryPattern(rule.FilePatterns, rel) {
			return true
		}
	}
	return false
}

// validateBackendBoundaryGuardFiles 证明每个声明测试名都存在于指定 guard 文件且可被 testing 发现。
func validateBackendBoundaryGuardFiles(root string, guards []BackendBoundaryGuard) []string {
	var violations []string
	for _, guard := range guards {
		if !isCanonicalBackendBoundaryGuardFile(guard.File) {
			continue
		}
		path, err := resolveBackendBoundaryGuardFile(root, guard.File)
		if err != nil {
			violations = append(violations, fmt.Sprintf(
				"guard %q file %s must resolve to a regular file within internal/archtest: %v",
				guard.ID,
				guard.File,
				err,
			))
			continue
		}
		names, err := DiscoverRunnableGoTests(path)
		if err != nil {
			violations = append(violations, fmt.Sprintf("guard %q read %s: %v", guard.ID, guard.File, err))
			continue
		}
		available := make(map[string]bool, len(names))
		for _, name := range names {
			available[name] = true
		}
		for _, name := range guard.TestNames {
			if !available[name] {
				violations = append(violations, fmt.Sprintf("guard %q test %q is not a runnable top-level Go test in %s", guard.ID, name, guard.File))
			}
		}
	}
	return violations
}

// resolveBackendBoundaryGuardFile 解析 guard 实体路径，并拒绝符号链接或仓库外目标。
func resolveBackendBoundaryGuardFile(root, guardFile string) (string, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	archtestDir, err := filepath.EvalSymlinks(filepath.Join(resolvedRoot, "internal", "archtest"))
	if err != nil {
		return "", fmt.Errorf("resolve archtest directory: %w", err)
	}
	if !isPathWithinDirectory(resolvedRoot, archtestDir) {
		return "", fmt.Errorf("archtest directory escapes repository root")
	}
	path := filepath.Join(resolvedRoot, filepath.FromSlash(guardFile))
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("guard path is not a regular file")
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if !isPathWithinDirectory(archtestDir, resolvedPath) {
		return "", fmt.Errorf("guard path escapes internal/archtest")
	}
	return resolvedPath, nil
}

// isPathWithinDirectory 判断目标路径是否仍位于给定目录树内。
func isPathWithinDirectory(directory, path string) bool {
	rel, err := filepath.Rel(directory, path)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// DiscoverRunnableGoTests 使用 Go AST 返回指定文件中可由 testing 发现的顶层 Test 函数名。
func DiscoverRunnableGoTests(path string) ([]string, error) {
	matched, err := build.Default.MatchFile(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return nil, fmt.Errorf("match Go build constraints for %s: %w", path, err)
	}
	if !matched {
		return nil, nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && isRunnableGoTestFunction(function) {
			names = append(names, function.Name.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// isRunnableGoTestFunction 镜像 cmd/go 对顶层 Test 函数的名称、签名和泛型约束。
func isRunnableGoTestFunction(function *ast.FuncDecl) bool {
	if !hasRunnableGoTestSignature(function) {
		return false
	}
	return isTestingTParameter(function.Type.Params.List[0])
}

// hasRunnableGoTestSignature 校验 Go testing 要求的顶层函数外形。
func hasRunnableGoTestSignature(function *ast.FuncDecl) bool {
	return function.Recv == nil && isGoTestName(function.Name.Name) &&
		(function.Type.TypeParams == nil || len(function.Type.TypeParams.List) == 0) &&
		(function.Type.Results == nil || len(function.Type.Results.List) == 0) && function.Type.Params != nil &&
		len(function.Type.Params.List) == 1 && len(function.Type.Params.List[0].Names) <= 1
}

// isTestingTParameter 镜像 cmd/go，接受 *T 或任意包选择器的 *pkg.T 外形。
func isTestingTParameter(field *ast.Field) bool {
	star, ok := field.Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	if ident, ok := star.X.(*ast.Ident); ok {
		return ident.Name == "T"
	}
	selector, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return selector.Sel.Name == "T"
}

func isGoTestName(name string) bool {
	if !strings.HasPrefix(name, "Test") {
		return false
	}
	if len(name) == len("Test") {
		return true
	}
	r, _ := utf8.DecodeRuneInString(strings.TrimPrefix(name, "Test"))
	return !unicode.IsLower(r)
}
