package archtest

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ValidateBackendBoundaryRegistry 返回所有可静态发现的 registry 配置错误。
func ValidateBackendBoundaryRegistry(registry BackendBoundaryRegistry) []string {
	owners := make(map[BoundaryOwnerID]bool, len(registry.Owners))
	var violations []string
	for i, owner := range registry.Owners {
		label := fmt.Sprintf("owner[%d]", i)
		if strings.TrimSpace(string(owner.ID)) == "" {
			violations = append(violations, label+" id is empty")
		} else if owners[owner.ID] {
			violations = append(violations, label+" duplicate owner "+string(owner.ID))
		} else {
			owners[owner.ID] = true
		}
		if strings.TrimSpace(owner.Reason) == "" {
			violations = append(violations, label+" reason is empty")
		}
		violations = append(violations, validateBoundaryPatterns(label+" file_patterns", owner.FilePatterns)...)
	}

	ruleIDs := make(map[BoundaryRuleID]bool, len(registry.Rules))
	for i, rule := range registry.Rules {
		label := fmt.Sprintf("rule[%d]", i)
		violations = append(violations, validateBackendBoundaryRule(label, rule, owners, ruleIDs)...)
		if strings.TrimSpace(string(rule.ID)) != "" {
			ruleIDs[rule.ID] = true
		}
	}
	return violations
}

// validateBackendBoundaryRule 校验单条规则的头字段、策略和例外，防止坏配置进入求值器。
func validateBackendBoundaryRule(label string, rule BackendBoundaryRule, owners map[BoundaryOwnerID]bool, ruleIDs map[BoundaryRuleID]bool) []string {
	violations := validateBackendBoundaryRuleHeader(label, rule, owners, ruleIDs)
	violations = append(violations, validateBoundaryPatterns(label+" file_patterns", rule.FilePatterns)...)
	violations = append(violations, validateBackendBoundaryRuleRequirements(label, rule)...)
	violations = append(violations, validateBoundaryImportPolicies(label+" allow", rule.Allow, owners)...)
	violations = append(violations, validateBoundaryImportPolicies(label+" deny", rule.Deny, owners)...)
	violations = append(violations, validateBoundaryFilePolicies(label+" scope_allow", rule.ScopeAllow, owners)...)
	violations = append(violations, validateBoundaryExceptions(label+" exception", rule.Exceptions, owners)...)
	violations = append(violations, validateBoundaryPolicyConflicts(label, rule)...)
	return violations
}

// validateBackendBoundaryRuleHeader 验证规则标识、owner、原因和求值类型等基础约束。
func validateBackendBoundaryRuleHeader(label string, rule BackendBoundaryRule, owners map[BoundaryOwnerID]bool, ruleIDs map[BoundaryRuleID]bool) []string {
	var violations []string
	if strings.TrimSpace(string(rule.ID)) == "" {
		violations = append(violations, label+" id is empty")
	} else if ruleIDs[rule.ID] {
		violations = append(violations, label+" duplicate rule "+string(rule.ID))
	}
	if strings.TrimSpace(string(rule.Owner)) == "" {
		violations = append(violations, label+" owner is empty")
	} else if !owners[rule.Owner] {
		violations = append(violations, label+" unknown owner "+string(rule.Owner))
	}
	if strings.TrimSpace(rule.Reason) == "" {
		violations = append(violations, label+" reason is empty")
	}
	if !isKnownBoundaryRuleKind(rule.Kind) {
		violations = append(violations, label+" has unknown kind "+string(rule.Kind))
	}
	return violations
}

func validateBackendBoundaryRuleRequirements(label string, rule BackendBoundaryRule) []string {
	var violations []string
	if ruleRequiresDeny(rule.Kind) && len(rule.Deny) == 0 {
		violations = append(violations, label+" must declare deny policies")
	}
	if rule.Kind == BoundaryRuleAllowInternalImports && len(rule.Allow) == 0 {
		violations = append(violations, label+" must declare allow policies")
	}
	return violations
}

func isKnownBoundaryRuleKind(kind BoundaryRuleKind) bool {
	switch kind {
	case BoundaryRuleDenyImports, BoundaryRuleAllowInternalImports, BoundaryRuleModuleSiblings, BoundaryRuleScopedImport:
		return true
	default:
		return false
	}
}

func ruleRequiresDeny(kind BoundaryRuleKind) bool {
	return kind == BoundaryRuleDenyImports || kind == BoundaryRuleScopedImport
}

func validateBoundaryPatterns(label string, patterns []string) []string {
	if len(patterns) == 0 {
		return []string{label + " are empty"}
	}
	seen := make(map[string]bool, len(patterns))
	var violations []string
	for i, pattern := range patterns {
		trimmed := strings.TrimSpace(pattern)
		if trimmed == "" {
			violations = append(violations, fmt.Sprintf("%s[%d] is empty", label, i))
			continue
		}
		if seen[trimmed] {
			violations = append(violations, fmt.Sprintf("%s[%d] duplicates %q", label, i, trimmed))
		}
		seen[trimmed] = true
	}
	return violations
}

// validateBoundaryImportPolicies 校验导入策略的 owner、文件范围和前缀唯一性。
func validateBoundaryImportPolicies(label string, policies []BoundaryImportPolicy, owners map[BoundaryOwnerID]bool) []string {
	seen := make(map[string]bool, len(policies))
	var violations []string
	for i, policy := range policies {
		item := fmt.Sprintf("%s[%d]", label, i)
		violations = append(violations, validateBoundaryPolicyOwner(item, policy.Owner, owners)...)
		if strings.TrimSpace(policy.FilePattern) == "" {
			violations = append(violations, item+" file_pattern is empty")
		}
		if strings.TrimSpace(policy.ImportPrefix) == "" {
			violations = append(violations, item+" import_prefix is empty")
		}
		if strings.TrimSpace(policy.Reason) == "" {
			violations = append(violations, item+" reason is empty")
		}
		key := string(policy.Owner) + "\x00" + policy.FilePattern + "\x00" + policy.ImportPrefix
		if seen[key] {
			violations = append(violations, item+" duplicates import policy")
		}
		seen[key] = true
	}
	return violations
}

func validateBoundaryFilePolicies(label string, policies []BoundaryFilePolicy, owners map[BoundaryOwnerID]bool) []string {
	seen := make(map[string]bool, len(policies))
	var violations []string
	for i, policy := range policies {
		item := fmt.Sprintf("%s[%d]", label, i)
		violations = append(violations, validateBoundaryPolicyOwner(item, policy.Owner, owners)...)
		if strings.TrimSpace(policy.FilePattern) == "" {
			violations = append(violations, item+" file_pattern is empty")
		}
		if strings.TrimSpace(policy.Reason) == "" {
			violations = append(violations, item+" reason is empty")
		}
		key := string(policy.Owner) + "\x00" + policy.FilePattern
		if seen[key] {
			violations = append(violations, item+" duplicates file policy")
		}
		seen[key] = true
	}
	return violations
}

// validateBoundaryExceptions 校验例外的精确范围和临时例外的移除条件。
func validateBoundaryExceptions(label string, exceptions []BoundaryException, owners map[BoundaryOwnerID]bool) []string {
	seen := make(map[string]bool, len(exceptions))
	var violations []string
	for i, exception := range exceptions {
		item := fmt.Sprintf("%s[%d]", label, i)
		violations = append(violations, validateBoundaryException(item, exception, seen, owners)...)
		seen[exception.ID] = true
	}
	return violations
}

// validateBoundaryException 验证单个例外的唯一标识、归属、精确范围与生命周期字段。
func validateBoundaryException(item string, exception BoundaryException, seen map[string]bool, owners map[BoundaryOwnerID]bool) []string {
	var violations []string
	if strings.TrimSpace(exception.ID) == "" {
		violations = append(violations, item+" id is empty")
	} else if seen[exception.ID] {
		violations = append(violations, item+" duplicate exception "+exception.ID)
	}
	violations = append(violations, validateBoundaryPolicyOwner(item, exception.Owner, owners)...)
	if strings.TrimSpace(exception.FilePattern) == "" {
		violations = append(violations, item+" file_pattern is empty")
	}
	if strings.TrimSpace(exception.ImportPrefix) == "" {
		violations = append(violations, item+" import_prefix is empty")
	}
	if strings.TrimSpace(exception.Reason) == "" {
		violations = append(violations, item+" reason is empty")
	}
	if exception.Class != BoundaryExceptionPermanent && exception.Class != BoundaryExceptionTemporary {
		violations = append(violations, item+" has unknown exception class "+string(exception.Class))
	}
	if exception.Class == BoundaryExceptionTemporary && strings.TrimSpace(exception.RemoveWhen) == "" {
		violations = append(violations, item+" temporary exception missing remove_when")
	}
	return violations
}

func validateBoundaryPolicyOwner(label string, owner BoundaryOwnerID, owners map[BoundaryOwnerID]bool) []string {
	if strings.TrimSpace(string(owner)) == "" {
		return []string{label + " owner is empty"}
	}
	if !owners[owner] {
		return []string{label + " unknown owner " + string(owner)}
	}
	return nil
}

func validateBoundaryPolicyConflicts(label string, rule BackendBoundaryRule) []string {
	allow := make(map[string]bool, len(rule.Allow))
	for _, policy := range rule.Allow {
		allow[policy.FilePattern+"\x00"+policy.ImportPrefix] = true
	}
	var violations []string
	for _, policy := range rule.Deny {
		key := policy.FilePattern + "\x00" + policy.ImportPrefix
		if allow[key] {
			violations = append(violations, label+" allow and deny the same import policy "+policy.FilePattern+" "+policy.ImportPrefix)
		}
	}
	return violations
}

// EvaluateBackendBoundary 解析选中规则覆盖的生产源码，并在根、语法或覆盖不完整时立即失败。
func EvaluateBackendBoundary(root string, registry BackendBoundaryRegistry, ruleIDs ...BoundaryRuleID) (BoundaryEvaluation, error) {
	rules, paths, err := prepareBackendBoundaryEvaluation(root, registry, ruleIDs)
	if err != nil {
		return BoundaryEvaluation{}, err
	}
	evaluation, err := evaluateBackendBoundaryPaths(root, paths, rules)
	if err != nil {
		return BoundaryEvaluation{}, err
	}
	if err := ensureBackendBoundaryCoverage(root, rules, evaluation); err != nil {
		return BoundaryEvaluation{}, err
	}
	return evaluation, nil
}

// prepareBackendBoundaryEvaluation 在扫描前一次性校验根目录、registry 和选中规则，避免部分求值。
func prepareBackendBoundaryEvaluation(root string, registry BackendBoundaryRegistry, ruleIDs []BoundaryRuleID) ([]BackendBoundaryRule, []string, error) {
	if info, err := os.Stat(root); err != nil {
		return nil, nil, fmt.Errorf("stat backend boundary root %s: %w", root, err)
	} else if !info.IsDir() {
		return nil, nil, fmt.Errorf("backend boundary root %s is not a directory", root)
	}
	if violations := ValidateBackendBoundaryRegistry(registry); len(violations) > 0 {
		return nil, nil, fmt.Errorf("invalid backend boundary registry:\n%s", strings.Join(violations, "\n"))
	}
	rules, err := selectBackendBoundaryRules(registry, ruleIDs)
	if err != nil {
		return nil, nil, err
	}
	paths, err := collectBackendBoundaryGoFiles(root)
	if err != nil {
		return nil, nil, err
	}
	return rules, paths, nil
}

// evaluateBackendBoundaryPaths 逐文件构建覆盖计数；任一候选文件解析失败都会阻断结果。
func evaluateBackendBoundaryPaths(root string, paths []string, rules []BackendBoundaryRule) (BoundaryEvaluation, error) {
	evaluation := BoundaryEvaluation{ByRule: make(map[BoundaryRuleID]int)}
	for _, path := range paths {
		candidate, err := parseBackendBoundaryCandidate(root, path, rules)
		if err != nil {
			return BoundaryEvaluation{}, err
		}
		if len(candidate.rules) == 0 {
			continue
		}
		if candidate.generated {
			evaluation.Excluded = append(evaluation.Excluded, candidate.rel)
			continue
		}
		evaluation.CandidateFiles++
		evaluation.MatchedFiles++
		for _, rule := range candidate.rules {
			evaluation.ByRule[rule.ID]++
			evaluation.Violations = append(evaluation.Violations, evaluateBackendBoundaryRule(rule, candidate.rel, candidate.imports)...)
		}
	}
	return evaluation, nil
}

type backendBoundaryCandidate struct {
	rel       string
	imports   []string
	rules     []BackendBoundaryRule
	generated bool
}

// parseBackendBoundaryCandidate 仅解析至少命中一条规则的源码，生成文件保持排除证据而不参与判断。
func parseBackendBoundaryCandidate(root, path string, rules []BackendBoundaryRule) (backendBoundaryCandidate, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return backendBoundaryCandidate{}, fmt.Errorf("relative path for %s: %w", path, err)
	}
	rel = filepath.ToSlash(rel)
	applicable := applicableBackendBoundaryRules(rules, rel)
	if len(applicable) == 0 {
		return backendBoundaryCandidate{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return backendBoundaryCandidate{}, fmt.Errorf("read %s: %w", rel, err)
	}
	if IsGeneratedSQLCFile(rel, data) {
		return backendBoundaryCandidate{rel: rel, rules: applicable, generated: true}, nil
	}
	imports, err := parseBackendBoundaryImports(path, data)
	if err != nil {
		return backendBoundaryCandidate{}, fmt.Errorf("parse %s: %w", rel, err)
	}
	return backendBoundaryCandidate{rel: rel, imports: imports, rules: applicable}, nil
}

func ensureBackendBoundaryCoverage(root string, rules []BackendBoundaryRule, evaluation BoundaryEvaluation) error {
	if evaluation.CandidateFiles == 0 {
		return fmt.Errorf("backend boundary evaluation found zero candidate production Go files under %s", root)
	}
	for _, rule := range rules {
		if evaluation.ByRule[rule.ID] == 0 {
			return fmt.Errorf("backend boundary rule %s matched zero production Go files under %s", rule.ID, root)
		}
	}
	return nil
}

// EvaluateBackendBoundaryFile 对单一源码文件应用选中规则，供 fail-fast 夹具和守卫复用。
func EvaluateBackendBoundaryFile(path, rel string, registry BackendBoundaryRegistry, ruleIDs ...BoundaryRuleID) ([]string, error) {
	if strings.TrimSpace(rel) == "" {
		return nil, fmt.Errorf("backend boundary relative path is empty")
	}
	if violations := ValidateBackendBoundaryRegistry(registry); len(violations) > 0 {
		return nil, fmt.Errorf("invalid backend boundary registry:\n%s", strings.Join(violations, "\n"))
	}
	rules, err := selectBackendBoundaryRules(registry, ruleIDs)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	if IsGeneratedSQLCFile(filepath.ToSlash(rel), data) {
		return nil, nil
	}
	imports, err := parseBackendBoundaryImports(path, data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	var violationsOut []string
	for _, rule := range applicableBackendBoundaryRules(rules, filepath.ToSlash(rel)) {
		violationsOut = append(violationsOut, evaluateBackendBoundaryRule(rule, filepath.ToSlash(rel), imports)...)
	}
	return violationsOut, nil
}

// selectBackendBoundaryRules 保持调用方选择顺序，并拒绝未知或重复的规则 ID。
func selectBackendBoundaryRules(registry BackendBoundaryRegistry, ids []BoundaryRuleID) ([]BackendBoundaryRule, error) {
	if len(ids) == 0 {
		rules := make([]BackendBoundaryRule, len(registry.Rules))
		for i, rule := range registry.Rules {
			rules[i] = cloneBackendBoundaryRule(rule)
		}
		return rules, nil
	}
	rules := make([]BackendBoundaryRule, 0, len(ids))
	seen := make(map[BoundaryRuleID]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return nil, fmt.Errorf("backend boundary rule %s was selected more than once", id)
		}
		seen[id] = true
		rule, ok := registry.Rule(id)
		if !ok {
			return nil, fmt.Errorf("unknown backend boundary rule %s", id)
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// collectBackendBoundaryGoFiles 遵循 archtest 的跳过目录约定收集候选 Go 源码。
func collectBackendBoundaryGoFiles(root string) ([]string, error) {
	skipDirs := DefaultSkipDirs()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && skipDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".go" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk backend boundary root %s: %w", root, err)
	}
	sort.Strings(files)
	return files, nil
}

func parseBackendBoundaryImports(path string, data []byte) ([]string, error) {
	node, err := parser.ParseFile(token.NewFileSet(), path, data, 0)
	if err != nil {
		return nil, err
	}
	imports := make([]string, 0, len(node.Imports))
	for _, spec := range node.Imports {
		imp, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("unquote import %s: %w", spec.Path.Value, err)
		}
		imports = append(imports, imp)
	}
	return imports, nil
}

func applicableBackendBoundaryRules(rules []BackendBoundaryRule, rel string) []BackendBoundaryRule {
	var applicable []BackendBoundaryRule
	for _, rule := range rules {
		if rule.SkipTestFiles && strings.HasSuffix(rel, "_test.go") {
			continue
		}
		if matchesAnyBackendBoundaryPattern(rule.FilePatterns, rel) {
			applicable = append(applicable, rule)
		}
	}
	return applicable
}

func evaluateBackendBoundaryRule(rule BackendBoundaryRule, rel string, imports []string) []string {
	switch rule.Kind {
	case BoundaryRuleDenyImports:
		return denyImportViolations(rule, rel, imports)
	case BoundaryRuleAllowInternalImports:
		return allowInternalImportViolations(rule, rel, imports)
	case BoundaryRuleModuleSiblings:
		return moduleSiblingImportViolationsForRule(rule, rel, imports)
	case BoundaryRuleScopedImport:
		return scopedImportViolations(rule, rel, imports)
	default:
		return []string{fmt.Sprintf("%s has unknown backend boundary rule kind %q", rule.ID, rule.Kind)}
	}
}

func denyImportViolations(rule BackendBoundaryRule, rel string, imports []string) []string {
	var violations []string
	for _, imp := range imports {
		if matchesBackendBoundaryPolicy(rule.Deny, rel, imp) && !backendBoundaryExceptionAllows(rule.Exceptions, rel, imp) {
			violations = append(violations, backendBoundaryViolation(rule, rel, imp))
		}
	}
	return violations
}

// allowInternalImportViolations 只对白名单外的内部或 cmd 导入报告违规，保留外部依赖边界。
func allowInternalImportViolations(rule BackendBoundaryRule, rel string, imports []string) []string {
	var violations []string
	for _, imp := range imports {
		if !isBackendBoundaryInternalOrCmdImport(imp) || isOwnMCPCommandImport(rel, imp) {
			continue
		}
		if matchesBackendBoundaryPolicy(rule.Deny, rel, imp) || !matchesBackendBoundaryPolicy(rule.Allow, rel, imp) {
			violations = append(violations, backendBoundaryViolation(rule, rel, imp))
		}
	}
	return violations
}

func moduleSiblingImportViolationsForRule(rule BackendBoundaryRule, rel string, imports []string) []string {
	owner, ok := backendBoundaryModuleOwner(rel)
	if !ok {
		return nil
	}
	var violations []string
	for _, imp := range imports {
		importOwner, ok := backendBoundaryImportedModuleOwner(imp)
		if ok && importOwner != owner {
			violations = append(violations, backendBoundaryViolation(rule, rel, imp))
		}
	}
	return violations
}

func scopedImportViolations(rule BackendBoundaryRule, rel string, imports []string) []string {
	var violations []string
	for _, imp := range imports {
		if !matchesBackendBoundaryPolicy(rule.Deny, rel, imp) {
			continue
		}
		if matchesBackendBoundaryFilePolicy(rule.ScopeAllow, rel) || backendBoundaryExceptionAllows(rule.Exceptions, rel, imp) {
			continue
		}
		violations = append(violations, backendBoundaryViolation(rule, rel, imp))
	}
	return violations
}

func matchesBackendBoundaryPolicy(policies []BoundaryImportPolicy, rel, imp string) bool {
	for _, policy := range policies {
		if matchesBackendBoundaryPattern(policy.FilePattern, rel) && matchesBackendBoundaryImportPrefix(imp, policy.ImportPrefix) {
			return true
		}
	}
	return false
}

func matchesBackendBoundaryFilePolicy(policies []BoundaryFilePolicy, rel string) bool {
	for _, policy := range policies {
		if matchesBackendBoundaryPattern(policy.FilePattern, rel) {
			return true
		}
	}
	return false
}

func backendBoundaryExceptionAllows(exceptions []BoundaryException, rel, imp string) bool {
	for _, exception := range exceptions {
		if matchesBackendBoundaryPattern(exception.FilePattern, rel) && matchesBackendBoundaryImportPrefix(imp, exception.ImportPrefix) {
			return true
		}
	}
	return false
}

func matchesAnyBackendBoundaryPattern(patterns []string, rel string) bool {
	for _, pattern := range patterns {
		if matchesBackendBoundaryPattern(pattern, rel) {
			return true
		}
	}
	return false
}

func matchesBackendBoundaryPattern(pattern, rel string) bool {
	pattern = filepath.ToSlash(strings.TrimPrefix(pattern, "./"))
	rel = filepath.ToSlash(strings.TrimPrefix(rel, "./"))
	switch {
	case strings.HasSuffix(pattern, "/**/*.go"):
		base := strings.TrimSuffix(pattern, "/**/*.go")
		return strings.HasPrefix(rel, base+"/") && strings.HasSuffix(rel, ".go")
	case strings.HasSuffix(pattern, "/**"):
		base := strings.TrimSuffix(pattern, "/**")
		return rel == base || strings.HasPrefix(rel, base+"/")
	default:
		return rel == pattern
	}
}

// matchesBackendBoundaryImportPrefix 统一处理仓库内前缀、cmd 前缀和外部完整导入路径。
func matchesBackendBoundaryImportPrefix(imp, prefix string) bool {
	prefix = strings.Trim(prefix, "/")
	if prefix == "frontend-app" {
		return strings.Contains(imp, "/frontend-app") || strings.HasPrefix(imp, "frontend-app")
	}
	if prefix == "cmd" || strings.HasPrefix(prefix, "cmd/") || strings.HasPrefix(prefix, "internal/") {
		prefix = backendBoundaryModulePath + "/" + prefix
	}
	return imp == prefix || strings.HasPrefix(imp, prefix+"/")
}

func isBackendBoundaryInternalOrCmdImport(imp string) bool {
	return strings.HasPrefix(imp, backendBoundaryModulePath+"/internal/") || strings.HasPrefix(imp, backendBoundaryModulePath+"/cmd/")
}

func isOwnMCPCommandImport(rel, imp string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 || parts[0] != "cmd" || !strings.HasPrefix(parts[1], "mcp-") {
		return false
	}
	prefix := backendBoundaryModulePath + "/cmd/" + parts[1]
	return imp == prefix || strings.HasPrefix(imp, prefix+"/")
}

// backendBoundaryModuleOwner 返回 module 生产文件的一层 owner，装配 module.go 不参与横向依赖判断。
func backendBoundaryModuleOwner(rel string) (string, bool) {
	if strings.HasSuffix(rel, "_test.go") || filepath.Base(rel) == "module.go" {
		return "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 3 || parts[0] != "internal" || parts[1] != "module" {
		return "", false
	}
	return parts[2], true
}

func backendBoundaryImportedModuleOwner(imp string) (string, bool) {
	prefix := backendBoundaryModulePath + "/internal/module/"
	if !strings.HasPrefix(imp, prefix) {
		return "", false
	}
	return strings.Split(strings.TrimPrefix(imp, prefix), "/")[0], true
}

func backendBoundaryViolation(rule BackendBoundaryRule, rel, imp string) string {
	return fmt.Sprintf("%s imports %s (rule=%s owner=%s reason=%s)", rel, imp, rule.ID, rule.Owner, rule.Reason)
}
