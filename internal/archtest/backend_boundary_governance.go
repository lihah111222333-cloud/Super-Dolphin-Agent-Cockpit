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
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidateBackendBoundaryGovernance 校验后端顶层目录、canonical rule 与真实专项测试入口的一致性。
func ValidateBackendBoundaryGovernance(root string, registry BackendBoundaryRegistry) []string {
	positions, err := canonicalBackendBoundaryRegistryPositions(root, registry)
	if err != nil {
		return []string{err.Error()}
	}
	violations := prefixBackendBoundaryRegistryViolations(registry, positions, ValidateBackendBoundaryRegistry(registry))
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

func canonicalBackendBoundaryRegistryPositions(root string, registry BackendBoundaryRegistry) (map[string][]token.Position, error) {
	if !isCanonicalBackendBoundaryRegistryShape(registry) {
		return nil, nil
	}
	sourcePath := filepath.Join(root, filepath.FromSlash(registry.canonicalSource))
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("%s: read canonical backend boundary registry source: %w", registry.canonicalSource, err)
	}
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, sourcePath, source, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: parse canonical backend boundary registry source: %w", registry.canonicalSource, err)
	}
	return extractBackendBoundaryRegistryPositions(fileSet, file, registry)
}

func isCanonicalBackendBoundaryRegistryShape(registry BackendBoundaryRegistry) bool {
	if registry.canonicalSource != defaultBackendBoundaryRegistrySource {
		return false
	}
	canonical := defaultBackendBoundaryRegistry()
	return len(registry.Owners) == len(canonical.Owners) &&
		len(registry.Rules) == len(canonical.Rules) &&
		len(registry.Guards) == len(canonical.Guards) &&
		len(registry.Surfaces) == len(canonical.Surfaces)
}

// extractBackendBoundaryRegistryPositions 按 canonical section/index 提取物理源码位置，源码结构漂移时立即失败。
func extractBackendBoundaryRegistryPositions(fileSet *token.FileSet, file *ast.File, registry BackendBoundaryRegistry) (map[string][]token.Position, error) {
	helpers, err := backendBoundaryRegistrySectionHelpers(file)
	if err != nil {
		return nil, fmt.Errorf("%s: locate canonical registry sections: %w", registry.canonicalSource, err)
	}
	sections := []struct {
		field string
		label string
		count int
	}{
		{field: "Owners", label: "owner", count: len(registry.Owners)},
		{field: "Rules", label: "rule", count: len(registry.Rules)},
		{field: "Guards", label: "guard", count: len(registry.Guards)},
		{field: "Surfaces", label: "surface", count: len(registry.Surfaces)},
	}
	positions := make(map[string][]token.Position, len(sections))
	for _, section := range sections {
		entries, err := backendBoundaryRegistryHelperEntries(file, helpers[section.field])
		if err != nil {
			return nil, fmt.Errorf("%s: locate %s entries: %w", registry.canonicalSource, section.field, err)
		}
		if len(entries) != section.count {
			return nil, fmt.Errorf("%s: %s source entries=%d registry entries=%d", registry.canonicalSource, section.field, len(entries), section.count)
		}
		for _, entry := range entries {
			positions[section.label] = append(positions[section.label], fileSet.PositionFor(entry.Pos(), false))
		}
	}
	return positions, nil
}

// backendBoundaryRegistrySectionHelpers 从 registry composite literal 解析四个 section 的 helper，避免复制 helper 名称事实。
func backendBoundaryRegistrySectionHelpers(file *ast.File) (map[string]string, error) {
	function := backendBoundaryNamedFunction(file, "defaultBackendBoundaryRegistry")
	literal, err := backendBoundaryReturnedComposite(function)
	if err != nil {
		return nil, err
	}
	helpers := make(map[string]string)
	for _, element := range literal.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, keyOK := keyValue.Key.(*ast.Ident)
		call, callOK := keyValue.Value.(*ast.CallExpr)
		if !keyOK || !callOK {
			continue
		}
		helper, ok := call.Fun.(*ast.Ident)
		if ok {
			helpers[key.Name] = helper.Name
		}
	}
	for _, field := range []string{"Owners", "Rules", "Guards", "Surfaces"} {
		if helpers[field] == "" {
			return nil, fmt.Errorf("section %s is not initialized by a helper call", field)
		}
	}
	return helpers, nil
}

func backendBoundaryRegistryHelperEntries(file *ast.File, helperName string) ([]ast.Expr, error) {
	literal, err := backendBoundaryReturnedComposite(backendBoundaryNamedFunction(file, helperName))
	if err != nil {
		return nil, fmt.Errorf("helper %s: %w", helperName, err)
	}
	return literal.Elts, nil
}

func backendBoundaryNamedFunction(file *ast.File, name string) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			return function
		}
	}
	return nil
}

// backendBoundaryReturnedComposite 只接受直接返回的 composite literal，使不受支持的 registry 结构变更 fail-fast。
func backendBoundaryReturnedComposite(function *ast.FuncDecl) (*ast.CompositeLit, error) {
	if function == nil || function.Body == nil {
		return nil, fmt.Errorf("function declaration is missing")
	}
	for _, statement := range function.Body.List {
		returned, ok := statement.(*ast.ReturnStmt)
		if !ok || len(returned.Results) != 1 {
			continue
		}
		literal, ok := returned.Results[0].(*ast.CompositeLit)
		if ok {
			return literal, nil
		}
	}
	return nil, fmt.Errorf("direct composite return is missing")
}

func prefixBackendBoundaryRegistryViolations(registry BackendBoundaryRegistry, positions map[string][]token.Position, violations []string) []string {
	result := make([]string, len(violations))
	for index, violation := range violations {
		section, entry, ok := backendBoundaryRegistryViolationEntry(violation)
		if !ok {
			result[index] = violation
			continue
		}
		if positions == nil {
			result[index] = "synthetic registry: " + violation
			continue
		}
		position := positions[section][entry]
		result[index] = fmt.Sprintf("%s:%d:%d: %s", registry.canonicalSource, position.Line, position.Column, violation)
	}
	return result
}

func backendBoundaryRegistryViolationEntry(violation string) (string, int, bool) {
	for _, section := range []string{"owner", "rule", "guard", "surface"} {
		prefix := section + "["
		if !strings.HasPrefix(violation, prefix) {
			continue
		}
		end := strings.IndexByte(violation[len(prefix):], ']')
		if end < 0 {
			return "", 0, false
		}
		entry, err := strconv.Atoi(violation[len(prefix) : len(prefix)+end])
		return section, entry, err == nil
	}
	return "", 0, false
}

// validateBackendBoundaryGovernanceRegistry 检查不依赖文件系统的 guard 与 surface 引用不变量。
func validateBackendBoundaryGovernanceRegistry(registry BackendBoundaryRegistry) []string {
	rules := make(map[BoundaryRuleID]bool, len(registry.Rules))
	for _, rule := range registry.Rules {
		rules[rule.ID] = true
	}
	guards, violations := validateBackendBoundaryGuardDescriptors(registry.Guards)
	surfaceViolations, ruleReferences, guardReferences := validateBackendBoundarySurfaceDescriptors(registry.Surfaces, rules, guards)
	violations = append(violations, surfaceViolations...)
	violations = append(violations, validateBackendBoundaryGuardApplicability(registry.Guards, registry.Surfaces)...)
	for id := range rules {
		if ruleReferences[id] == 0 {
			violations = append(violations, fmt.Sprintf("rule %q is not referenced by any backend surface", id))
		}
	}
	for id := range guards {
		if guardReferences[id] == 0 {
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
		violations = append(violations, fmt.Sprintf("%s file %q must be a canonical internal/**/*_test.go path", label, guard.File))
	}
	if len(guard.TestNames) == 0 {
		violations = append(violations, label+" test_names is empty")
	}
	violations = append(violations, validateBackendBoundaryGuardAppliesTo(label, guard.AppliesTo)...)
	seen := make(map[string]bool, len(guard.TestNames))
	for _, name := range guard.TestNames {
		if !isGoTestName(name) {
			violations = append(violations, fmt.Sprintf("%s test name %q is not discoverable by Go testing", label, name))
		} else if seen[name] {
			violations = append(violations, fmt.Sprintf("%s duplicate test name %q", label, name))
		}
		seen[name] = true
	}
	violations = append(violations, validateBackendBoundaryGuardBuildTags(label, guard.BuildTags)...)
	return violations
}

// validateBackendBoundaryGuardAppliesTo 拒绝空、非规范或重复的 guard 适用 surface。
func validateBackendBoundaryGuardAppliesTo(label string, surfaces []BoundarySurfaceID) []string {
	if len(surfaces) == 0 {
		return []string{label + " applies_to is empty"}
	}
	seen := make(map[BoundarySurfaceID]bool, len(surfaces))
	var violations []string
	for _, surface := range surfaces {
		if !isCanonicalBackendBoundarySurface(string(surface)) {
			violations = append(violations, fmt.Sprintf("%s applies_to path %q is not a canonical backend surface", label, surface))
		} else if seen[surface] {
			violations = append(violations, fmt.Sprintf("%s duplicate applies_to surface %q", label, surface))
		}
		seen[surface] = true
	}
	return violations
}

// validateBackendBoundaryGuardBuildTags 拒绝非法或重复的专项测试构建标签。
func validateBackendBoundaryGuardBuildTags(label string, tags []string) []string {
	seen := make(map[string]bool, len(tags))
	var violations []string
	for _, tag := range tags {
		if !isCanonicalGoBuildTag(tag) {
			violations = append(violations, fmt.Sprintf("%s build tag %q is invalid", label, tag))
		} else if seen[tag] {
			violations = append(violations, fmt.Sprintf("%s duplicate build tag %q", label, tag))
		}
		seen[tag] = true
	}
	return violations
}

// isCanonicalGoBuildTag 只接受 Go 构建约束可使用的字母、数字、下划线和点。
func isCanonicalGoBuildTag(tag string) bool {
	if tag == "" {
		return false
	}
	for _, char := range tag {
		if !unicode.IsLetter(char) && !unicode.IsDigit(char) && char != '_' && char != '.' {
			return false
		}
	}
	return true
}

// validateBackendBoundarySurfaceDescriptors 拒绝空机制、重复目录以及未知 rule/guard 引用。
func validateBackendBoundarySurfaceDescriptors(
	items []BackendBoundarySurface,
	rules map[BoundaryRuleID]bool,
	guards map[BoundaryGuardID]bool,
) ([]string, map[BoundaryRuleID]int, map[BoundaryGuardID]int) {
	seen := make(map[string]bool, len(items))
	ruleReferences := make(map[BoundaryRuleID]int, len(rules))
	guardReferences := make(map[BoundaryGuardID]int, len(guards))
	var violations []string
	for i, surface := range items {
		label := fmt.Sprintf("surface[%d]", i)
		if !isCanonicalBackendBoundarySurface(surface.Path) {
			violations = append(violations, fmt.Sprintf("%s path %q must be cmd/<name>, internal/<name>, or pkg/<name>", label, surface.Path))
		} else if seen[surface.Path] {
			violations = append(violations, fmt.Sprintf("%s duplicate backend surface %q", label, surface.Path))
		}
		seen[surface.Path] = true
		violations = append(violations, validateBackendBoundarySurfaceFields(label, surface, rules, guards, ruleReferences, guardReferences)...)
	}
	return violations, ruleReferences, guardReferences
}

func validateBackendBoundarySurfaceFields(
	label string,
	surface BackendBoundarySurface,
	rules map[BoundaryRuleID]bool,
	guards map[BoundaryGuardID]bool,
	ruleReferences map[BoundaryRuleID]int,
	guardReferences map[BoundaryGuardID]int,
) []string {
	var violations []string
	if strings.TrimSpace(surface.Reason) == "" {
		violations = append(violations, label+" reason is empty")
	}
	if len(surface.RuleIDs) == 0 && len(surface.GuardIDs) == 0 {
		violations = append(violations, fmt.Sprintf("%s %q has no canonical rules or specialized guards", label, surface.Path))
	}
	violations = append(violations, validateBackendBoundarySurfaceRules(label, surface.RuleIDs, rules, ruleReferences)...)
	violations = append(violations, validateBackendBoundarySurfaceGuards(label, surface.GuardIDs, guards, guardReferences)...)
	return violations
}

// validateBackendBoundarySurfaceRules 拒绝 surface 中未知或重复的 canonical rule。
func validateBackendBoundarySurfaceRules(
	label string,
	ids []BoundaryRuleID,
	known map[BoundaryRuleID]bool,
	references map[BoundaryRuleID]int,
) []string {
	seen := make(map[BoundaryRuleID]bool, len(ids))
	var violations []string
	for _, id := range ids {
		if !known[id] {
			violations = append(violations, fmt.Sprintf("%s unknown rule %q", label, id))
		} else if seen[id] {
			violations = append(violations, fmt.Sprintf("%s duplicate rule %q", label, id))
		}
		seen[id] = true
		if known[id] {
			references[id]++
		}
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

// validateBackendBoundaryGuardApplicability 证明 guard 的 applies_to 与 surface 的 GuardIDs 双向一致。
func validateBackendBoundaryGuardApplicability(guards []BackendBoundaryGuard, surfaces []BackendBoundarySurface) []string {
	knownSurfaces := make(map[BoundarySurfaceID]bool, len(surfaces))
	references := make(map[BoundarySurfaceID]map[BoundaryGuardID]bool, len(surfaces))
	for _, surface := range surfaces {
		id := BoundarySurfaceID(surface.Path)
		knownSurfaces[id] = true
		references[id] = boundaryGuardIDSet(surface.GuardIDs)
	}
	applicability := make(map[BoundaryGuardID]map[BoundarySurfaceID]bool, len(guards))
	var violations []string
	for _, guard := range guards {
		applicability[guard.ID] = boundarySurfaceIDSet(guard.AppliesTo)
		for _, surface := range guard.AppliesTo {
			if !knownSurfaces[surface] {
				violations = append(violations, fmt.Sprintf("guard %q applies_to unknown backend surface %q", guard.ID, surface))
				continue
			}
			if !references[surface][guard.ID] {
				violations = append(violations, fmt.Sprintf("guard %q applies_to surface %q but surface does not reference guard", guard.ID, surface))
			}
		}
	}
	for _, surface := range surfaces {
		for _, guardID := range surface.GuardIDs {
			if !applicability[guardID][BoundarySurfaceID(surface.Path)] {
				violations = append(violations, fmt.Sprintf("surface %q guard %q is not declared in guard applies_to", surface.Path, guardID))
			}
		}
	}
	return violations
}

func boundaryGuardIDSet(ids []BoundaryGuardID) map[BoundaryGuardID]bool {
	result := make(map[BoundaryGuardID]bool, len(ids))
	for _, id := range ids {
		result[id] = true
	}
	return result
}

func boundarySurfaceIDSet(ids []BoundarySurfaceID) map[BoundarySurfaceID]bool {
	result := make(map[BoundarySurfaceID]bool, len(ids))
	for _, id := range ids {
		result[id] = true
	}
	return result
}

func isCanonicalBackendBoundaryGuardFile(path string) bool {
	return !strings.Contains(path, "\\") && path == filepath.ToSlash(filepath.Clean(path)) &&
		strings.HasPrefix(path, "internal/") && strings.HasSuffix(path, "_test.go")
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
				"guard %q file %s must resolve to a regular file within the repository internal tree: %v",
				guard.ID,
				guard.File,
				err,
			))
			continue
		}
		names, err := discoverRunnableGoTests(path, guard.BuildTags)
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

// resolveBackendBoundaryGuardFile 解析 guard 实体路径，并拒绝符号链接或仓库 internal 树外目标。
func resolveBackendBoundaryGuardFile(root, guardFile string) (string, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	internalDir, err := filepath.EvalSymlinks(filepath.Join(resolvedRoot, "internal"))
	if err != nil {
		return "", fmt.Errorf("resolve repository internal directory: %w", err)
	}
	if !isPathWithinDirectory(resolvedRoot, internalDir) {
		return "", fmt.Errorf("internal directory escapes repository root")
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
	if !isPathWithinDirectory(internalDir, resolvedPath) {
		return "", fmt.Errorf("guard path escapes repository internal tree")
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
	return discoverRunnableGoTests(path, nil)
}

// discoverRunnableGoTests 在当前 GOFLAGS 与 guard 专项标签合并后的构建上下文中发现测试。
func discoverRunnableGoTests(path string, additionalTags []string) ([]string, error) {
	buildContext, err := currentGoBuildContext(additionalTags)
	if err != nil {
		return nil, err
	}
	matched, err := buildContext.MatchFile(filepath.Dir(path), filepath.Base(path))
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

func currentGoBuildContext(additionalTags []string) (build.Context, error) {
	buildContext := build.Default
	tags, err := goBuildTagsFromGOFLAGS(os.Getenv("GOFLAGS"))
	if err != nil {
		return build.Context{}, err
	}
	buildContext.BuildTags = append(append(append([]string(nil), buildContext.BuildTags...), tags...), additionalTags...)
	return buildContext, nil
}

// goBuildTagsFromGOFLAGS 提取最后一个 -tags 设置，并对标签内容做 fail-fast 校验。
func goBuildTagsFromGOFLAGS(flags string) ([]string, error) {
	fields, err := splitGoQuotedFields(flags)
	if err != nil {
		return nil, fmt.Errorf("parse GOFLAGS: %w", err)
	}
	value, err := lastGoBuildTagsValue(fields)
	if err != nil || value == "" {
		return nil, err
	}
	var tags []string
	for tag := range strings.FieldsFuncSeq(value, func(char rune) bool { return char == ',' || unicode.IsSpace(char) }) {
		if !isCanonicalGoBuildTag(tag) {
			return nil, fmt.Errorf("GOFLAGS contains invalid build tag %q", tag)
		}
		tags = append(tags, tag)
	}
	return tags, nil
}

// lastGoBuildTagsValue 校验 GOFLAGS 字段均为 flag，并返回最后一个 tags 设置。
func lastGoBuildTagsValue(fields []string) (string, error) {
	var value string
	for _, field := range fields {
		if !strings.HasPrefix(field, "-") {
			return "", fmt.Errorf("parse GOFLAGS: non-flag %q", field)
		}
		switch {
		case field == "-tags" || field == "--tags":
			return "", fmt.Errorf("GOFLAGS -tags is missing its =value")
		case strings.HasPrefix(field, "-tags="):
			value = strings.TrimPrefix(field, "-tags=")
		case strings.HasPrefix(field, "--tags="):
			value = strings.TrimPrefix(field, "--tags=")
		}
	}
	return value, nil
}

// splitGoQuotedFields 镜像 cmd/internal/quoted.Split，按 cmd/go 的 GOFLAGS 引号规则拆分参数。
func splitGoQuotedFields(input string) ([]string, error) {
	var fields []string
	for len(input) > 0 {
		input = strings.TrimLeftFunc(input, func(char rune) bool {
			return char == ' ' || char == '\t' || char == '\n' || char == '\r'
		})
		if input == "" {
			break
		}
		if input[0] == '\'' || input[0] == '"' {
			quote := input[0]
			closing := strings.IndexByte(input[1:], quote)
			if closing < 0 {
				return nil, fmt.Errorf("unterminated %c string", quote)
			}
			fields = append(fields, input[1:closing+1])
			input = input[closing+2:]
			continue
		}
		end := strings.IndexAny(input, " \t\n\r")
		if end < 0 {
			fields = append(fields, input)
			break
		}
		fields = append(fields, input[:end])
		input = input[end:]
	}
	return fields, nil
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
