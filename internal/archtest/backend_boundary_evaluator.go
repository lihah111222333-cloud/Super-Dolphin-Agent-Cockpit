package archtest

import (
	"fmt"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// ValidateBackendBoundaryRegistry 返回所有可静态发现的 registry 配置错误。
func ValidateBackendBoundaryRegistry(registry BackendBoundaryRegistry) []string {
	owners := make(map[BoundaryOwnerID]bool, len(registry.Owners))
	ownerPatterns := make(map[BoundaryOwnerID][]string, len(registry.Owners))
	var violations []string
	for i, owner := range registry.Owners {
		label := fmt.Sprintf("owner[%d]", i)
		if strings.TrimSpace(string(owner.ID)) == "" {
			violations = append(violations, label+" id is empty")
		} else if owners[owner.ID] {
			violations = append(violations, label+" duplicate owner "+string(owner.ID))
		} else {
			owners[owner.ID] = true
			ownerPatterns[owner.ID] = append([]string(nil), owner.FilePatterns...)
		}
		if strings.TrimSpace(owner.Reason) == "" {
			violations = append(violations, label+" reason is empty")
		}
		violations = append(violations, validateBoundaryPatterns(label+" file_patterns", owner.FilePatterns)...)
	}

	ruleIDs := make(map[BoundaryRuleID]bool, len(registry.Rules))
	for i, rule := range registry.Rules {
		label := fmt.Sprintf("rule[%d]", i)
		violations = append(violations, validateBackendBoundaryRule(label, rule, owners, ownerPatterns, ruleIDs)...)
		if strings.TrimSpace(string(rule.ID)) != "" {
			ruleIDs[rule.ID] = true
		}
	}
	violations = append(violations, validateBackendBoundaryGovernanceRegistry(registry)...)
	return violations
}

// validateBackendBoundaryRule 校验单条规则的头字段、策略和例外，防止坏配置进入求值器。
func validateBackendBoundaryRule(label string, rule BackendBoundaryRule, owners map[BoundaryOwnerID]bool, ownerPatterns map[BoundaryOwnerID][]string, ruleIDs map[BoundaryRuleID]bool) []string {
	violations := validateBackendBoundaryRuleHeader(label, rule, owners, ruleIDs)
	violations = append(violations, validateBoundaryPatterns(label+" file_patterns", rule.FilePatterns)...)
	violations = append(violations, validateBoundaryRuleOwnerPatterns(label, rule, ownerPatterns)...)
	violations = append(violations, validateBackendBoundaryRuleRequirements(label, rule)...)
	violations = append(violations, validateBoundaryImportPolicies(label+" allow", rule, rule.Allow, owners, true)...)
	violations = append(violations, validateBoundaryImportPolicies(label+" deny", rule, rule.Deny, owners, false)...)
	violations = append(violations, validateBoundaryFilePolicies(label+" scope_allow", rule, owners)...)
	violations = append(violations, validateBoundaryExceptions(label+" exception", rule, owners)...)
	violations = append(violations, validateBoundaryPolicyConflicts(label, rule)...)
	return violations
}

func validateBoundaryRuleOwnerPatterns(label string, rule BackendBoundaryRule, ownerPatterns map[BoundaryOwnerID][]string) []string {
	registered := ownerPatterns[rule.Owner]
	var violations []string
	for i, pattern := range rule.FilePatterns {
		if !slices.Contains(registered, pattern) {
			violations = append(violations, fmt.Sprintf("%s file_patterns[%d] rule file_pattern must be registered in owner file_patterns", label, i))
		}
	}
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

// validateBackendBoundaryRuleRequirements 按 typed kind 要求必要的 allow 或 deny 策略。
func validateBackendBoundaryRuleRequirements(label string, rule BackendBoundaryRule) []string {
	var violations []string
	if ruleRequiresDeny(rule.Kind) && len(rule.Deny) == 0 {
		violations = append(violations, label+" must declare deny policies")
	}
	if (rule.Kind == BoundaryRuleAllowInternalImports || rule.Kind == BoundaryRuleStoreImports) && len(rule.Allow) == 0 {
		violations = append(violations, label+" must declare allow policies")
	}
	return violations
}

func isKnownBoundaryRuleKind(kind BoundaryRuleKind) bool {
	switch kind {
	case BoundaryRuleDenyImports, BoundaryRuleAllowInternalImports, BoundaryRuleModuleSiblings, BoundaryRuleScopedImport, BoundaryRuleStoreImports:
		return true
	default:
		return false
	}
}

func ruleRequiresDeny(kind BoundaryRuleKind) bool {
	return kind == BoundaryRuleDenyImports || kind == BoundaryRuleScopedImport
}

// validateBoundaryPatterns 校验规则模式非空、唯一，并且仅使用求值器支持的 glob 语法。
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
		if !isSupportedBackendBoundaryPattern(trimmed) {
			violations = append(violations, fmt.Sprintf("%s[%d] has unsupported file pattern syntax %q", label, i, trimmed))
		}
		if seen[trimmed] {
			violations = append(violations, fmt.Sprintf("%s[%d] duplicates %q", label, i, trimmed))
		}
		seen[trimmed] = true
	}
	return violations
}

// isSupportedBackendBoundaryPattern 仅接受求值器明确实现的精确模式，拒绝任意 glob。
func isSupportedBackendBoundaryPattern(pattern string) bool {
	if !strings.ContainsAny(pattern, "*?[]") {
		return true
	}
	for _, suffix := range []string{"/**/*.go", "/**/module.go", "/**"} {
		if base, ok := strings.CutSuffix(pattern, suffix); ok {
			return base != "" && !strings.ContainsAny(base, "*?[]")
		}
	}
	if base, ok := strings.CutSuffix(pattern, "/*/main.go"); ok {
		return base != "" && !strings.ContainsAny(base, "*?[]")
	}
	return false
}

// validateBoundaryImportPolicies 校验导入策略的 owner、文件范围和前缀唯一性。
func validateBoundaryImportPolicies(label string, rule BackendBoundaryRule, policies []BoundaryImportPolicy, owners map[BoundaryOwnerID]bool, checkStatefulSidecarReason bool) []string {
	seen := make(map[string]bool, len(policies))
	var violations []string
	for i, policy := range policies {
		item := fmt.Sprintf("%s[%d]", label, i)
		violations = append(violations, validateBoundaryImportPolicyFields(item, rule, policy, owners)...)
		violations = append(violations, validateStatefulSidecarAllowance(item, policy, checkStatefulSidecarReason)...)
		key := string(policy.Owner) + "\x00" + policy.FilePattern + "\x00" + policy.ImportPrefix
		if seen[key] {
			violations = append(violations, item+" duplicates import policy")
		}
		seen[key] = true
	}
	return violations
}

// validateBoundaryImportPolicyFields 校验普通导入策略与所属规则的归属、范围和说明字段。
func validateBoundaryImportPolicyFields(item string, rule BackendBoundaryRule, policy BoundaryImportPolicy, owners map[BoundaryOwnerID]bool) []string {
	violations := validateBoundaryPolicyOwner(item, policy.Owner, owners)
	if policy.Owner != rule.Owner {
		violations = append(violations, item+" owner must match rule owner")
	}
	if strings.TrimSpace(policy.FilePattern) == "" {
		violations = append(violations, item+" file_pattern is empty")
	} else if !backendBoundaryPatternIsRegistered(rule.FilePatterns, policy.FilePattern) {
		violations = append(violations, item+" file_pattern must be registered in rule file_patterns")
	}
	if strings.TrimSpace(policy.ImportPrefix) == "" {
		violations = append(violations, item+" import_prefix is empty")
	} else if !isCanonicalBackendBoundaryImportPrefix(policy.ImportPrefix) {
		violations = append(violations, item+" import_prefix must use canonical form")
	}
	if strings.TrimSpace(policy.Reason) == "" {
		violations = append(violations, item+" reason is empty")
	}
	return violations
}

// validateStatefulSidecarAllowance 阻止 stateful 依赖以通用原因或祖先前缀扩大许可。
func validateStatefulSidecarAllowance(item string, policy BoundaryImportPolicy, enabled bool) []string {
	if !enabled {
		return nil
	}
	var violations []string
	if statefulSidecarAllowanceIsGeneric(policy) {
		violations = append(violations, item+" stateful sidecar allowance must name its sidecar")
	}
	if statefulSidecarAllowanceUsesAncestorPrefix(policy) {
		violations = append(violations, item+" stateful sidecar allowance must not use ancestor import prefix")
	}
	return violations
}

func backendBoundaryPatternIsRegistered(patterns []string, candidate string) bool {
	return slices.Contains(patterns, candidate)
}

// statefulSidecarAllowanceIsGeneric 防止 db 和 metrics 白名单以通用原因逃避 sidecar 审计。
func statefulSidecarAllowanceIsGeneric(policy BoundaryImportPolicy) bool {
	if !boundaryImportPrefixMatches(policy.ImportPrefix, "internal/platform/db") &&
		!boundaryImportPrefixMatches(policy.ImportPrefix, "internal/platform/metrics") {
		return false
	}
	if !strings.HasPrefix(policy.FilePattern, "cmd/mcp-") {
		return false
	}
	sidecar := strings.TrimSuffix(strings.TrimPrefix(policy.FilePattern, "cmd/mcp-"), "/**/*.go")
	return !strings.Contains(strings.ToLower(policy.Reason), statefulSidecarReasonName(sidecar)+" sidecar")
}

func statefulSidecarAllowanceUsesAncestorPrefix(policy BoundaryImportPolicy) bool {
	if !strings.HasPrefix(policy.FilePattern, "cmd/mcp-") {
		return false
	}
	for _, protected := range []string{"internal/platform/db", "internal/platform/metrics"} {
		if policy.ImportPrefix != protected && boundaryImportPrefixMatches(protected, policy.ImportPrefix) {
			return true
		}
	}
	return false
}

func boundaryImportPrefixMatches(got, want string) bool {
	got = normalizeBackendBoundaryImportPrefix(got)
	want = normalizeBackendBoundaryImportPrefix(want)
	return got == want || strings.HasPrefix(got, want+"/")
}

func statefulSidecarReasonName(sidecar string) string {
	if sidecar == "orch" {
		return "orchestration"
	}
	return sidecar
}

// validateBoundaryFilePolicies 校验范围放行只能使用 registry 注册的 scope，且归属和规则覆盖一致。
func validateBoundaryFilePolicies(label string, rule BackendBoundaryRule, owners map[BoundaryOwnerID]bool) []string {
	seen := make(map[string]bool, len(rule.ScopeAllow))
	var violations []string
	for i, policy := range rule.ScopeAllow {
		item := fmt.Sprintf("%s[%d]", label, i)
		violations = append(violations, validateBoundaryPolicyOwner(item, policy.Owner, owners)...)
		if policy.Owner != rule.Owner {
			violations = append(violations, item+" owner must match rule owner")
		}
		if strings.TrimSpace(policy.FilePattern) == "" {
			violations = append(violations, item+" file_pattern is empty")
		}
		expectedPattern, knownScope := boundaryScopeFilePattern(policy.Scope)
		if !knownScope || policy.FilePattern != expectedPattern {
			violations = append(violations, item+" scope_allow file_pattern is not a registered scope")
		} else if !matchesAnyBackendBoundaryPattern(rule.FilePatterns, policy.FilePattern) {
			violations = append(violations, item+" registered scope is outside rule file_patterns")
		}
		if strings.TrimSpace(policy.Reason) == "" {
			violations = append(violations, item+" reason is empty")
		}
		key := string(policy.Owner) + "\x00" + string(policy.Scope) + "\x00" + policy.FilePattern
		if seen[key] {
			violations = append(violations, item+" duplicates file policy")
		}
		seen[key] = true
	}
	return violations
}

// validateBoundaryExceptions 校验例外的精确范围和临时例外的移除条件。
func validateBoundaryExceptions(label string, rule BackendBoundaryRule, owners map[BoundaryOwnerID]bool) []string {
	seen := make(map[string]bool, len(rule.Exceptions))
	var violations []string
	for i, exception := range rule.Exceptions {
		item := fmt.Sprintf("%s[%d]", label, i)
		violations = append(violations, validateBoundaryException(item, rule, exception, seen, owners)...)
		seen[exception.ID] = true
	}
	return violations
}

// validateBoundaryException 验证单个例外的唯一标识、归属、精确范围与生命周期字段。
func validateBoundaryException(item string, rule BackendBoundaryRule, exception BoundaryException, seen map[string]bool, owners map[BoundaryOwnerID]bool) []string {
	violations := validateBoundaryExceptionIdentity(item, rule, exception, seen, owners)
	violations = append(violations, validateBoundaryExceptionScope(item, rule, exception)...)
	violations = append(violations, validateBoundaryExceptionLifecycle(item, exception)...)
	return violations
}

// validateBoundaryExceptionIdentity 校验例外 ID 与规则 owner 的一致性。
func validateBoundaryExceptionIdentity(item string, rule BackendBoundaryRule, exception BoundaryException, seen map[string]bool, owners map[BoundaryOwnerID]bool) []string {
	var violations []string
	if strings.TrimSpace(exception.ID) == "" {
		violations = append(violations, item+" id is empty")
	} else if seen[exception.ID] {
		violations = append(violations, item+" duplicate exception "+exception.ID)
	}
	violations = append(violations, validateBoundaryPolicyOwner(item, exception.Owner, owners)...)
	if exception.Owner != rule.Owner {
		violations = append(violations, item+" owner must match rule owner")
	}
	return violations
}

// validateBoundaryExceptionScope 校验例外只能收窄既有规则的文件与导入范围。
func validateBoundaryExceptionScope(item string, rule BackendBoundaryRule, exception BoundaryException) []string {
	var violations []string
	if strings.TrimSpace(exception.FilePattern) == "" {
		violations = append(violations, item+" file_pattern is empty")
	} else if strings.ContainsAny(exception.FilePattern, "*?[]") {
		violations = append(violations, item+" exception file_pattern must be exact")
	} else if !matchesAnyBackendBoundaryPattern(rule.FilePatterns, exception.FilePattern) {
		violations = append(violations, item+" exception file_pattern is outside rule file_patterns")
	}
	if strings.TrimSpace(exception.ImportPrefix) == "" {
		violations = append(violations, item+" import_prefix is empty")
	} else if !isCanonicalBackendBoundaryImportPrefix(exception.ImportPrefix) {
		violations = append(violations, item+" import_prefix must use canonical form")
	} else if !exceptionMatchesRuleDeny(rule, exception) {
		violations = append(violations, item+" import_prefix is outside rule deny policies")
	}
	return violations
}

// validateBoundaryExceptionLifecycle 校验例外原因、分类和临时移除条件。
func validateBoundaryExceptionLifecycle(item string, exception BoundaryException) []string {
	var violations []string
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

func exceptionMatchesRuleDeny(rule BackendBoundaryRule, exception BoundaryException) bool {
	for _, deny := range rule.Deny {
		if matchesBackendBoundaryPattern(deny.FilePattern, exception.FilePattern) &&
			matchesBackendBoundaryImportPrefix(exception.ImportPrefix, deny.ImportPrefix) {
			return true
		}
	}
	return false
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
	imports   []backendBoundaryImport
	rules     []BackendBoundaryRule
	generated bool
}

type backendBoundaryImport struct {
	path   string
	line   int
	column int
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

// parseBackendBoundaryImports 解析导入路径及其字符串字面量的精确源码位置。
func parseBackendBoundaryImports(path string, data []byte) ([]backendBoundaryImport, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, path, data, 0)
	if err != nil {
		return nil, err
	}
	imports := make([]backendBoundaryImport, 0, len(node.Imports))
	for _, spec := range node.Imports {
		imp, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("unquote import %s: %w", spec.Path.Value, err)
		}
		position := fset.PositionFor(spec.Path.Pos(), false)
		if !position.IsValid() || position.Line <= 0 || position.Column <= 0 {
			return nil, fmt.Errorf("locate import %s in %s", spec.Path.Value, path)
		}
		imports = append(imports, backendBoundaryImport{path: imp, line: position.Line, column: position.Column})
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

// evaluateBackendBoundaryRule 按已验证的 typed kind 分派规则，未知 kind 仍失败关闭。
func evaluateBackendBoundaryRule(rule BackendBoundaryRule, rel string, imports []backendBoundaryImport) []string {
	switch rule.Kind {
	case BoundaryRuleDenyImports:
		return denyImportViolations(rule, rel, imports)
	case BoundaryRuleAllowInternalImports:
		return allowInternalImportViolations(rule, rel, imports)
	case BoundaryRuleModuleSiblings:
		return moduleSiblingImportViolationsForRule(rule, rel, imports)
	case BoundaryRuleScopedImport:
		return scopedImportViolations(rule, rel, imports)
	case BoundaryRuleStoreImports:
		return storeImportViolationsForRule(rule, rel, imports)
	default:
		return []string{fmt.Sprintf("%s has unknown backend boundary rule kind %q", rule.ID, rule.Kind)}
	}
}

func denyImportViolations(rule BackendBoundaryRule, rel string, imports []backendBoundaryImport) []string {
	var violations []string
	for _, imp := range imports {
		if matchesBackendBoundaryPolicy(rule.Deny, rel, imp.path) && !backendBoundaryExceptionAllows(rule.Exceptions, rel, imp.path) {
			violations = append(violations, backendBoundaryViolation(rule, rel, imp))
		}
	}
	return violations
}

// allowInternalImportViolations 只对白名单外的内部或 cmd 导入报告违规，保留外部依赖边界。
func allowInternalImportViolations(rule BackendBoundaryRule, rel string, imports []backendBoundaryImport) []string {
	var violations []string
	for _, imp := range imports {
		if !isBackendBoundaryInternalOrCmdImport(imp.path) || isOwnMCPCommandImport(rel, imp.path) {
			continue
		}
		if matchesBackendBoundaryPolicy(rule.Deny, rel, imp.path) || !matchesBackendBoundaryPolicy(rule.Allow, rel, imp.path) {
			violations = append(violations, backendBoundaryViolation(rule, rel, imp))
		}
	}
	return violations
}

func moduleSiblingImportViolationsForRule(rule BackendBoundaryRule, rel string, imports []backendBoundaryImport) []string {
	owner, ok := backendBoundaryModuleOwner(rel)
	if !ok {
		return nil
	}
	var violations []string
	for _, imp := range imports {
		importOwner, ok := backendBoundaryImportedModuleOwner(imp.path)
		if ok && importOwner != owner {
			violations = append(violations, backendBoundaryViolation(rule, rel, imp))
		}
	}
	return violations
}

func scopedImportViolations(rule BackendBoundaryRule, rel string, imports []backendBoundaryImport) []string {
	var violations []string
	for _, imp := range imports {
		if !matchesBackendBoundaryPolicy(rule.Deny, rel, imp.path) {
			continue
		}
		if matchesBackendBoundaryFilePolicy(rule.ScopeAllow, rel) || backendBoundaryExceptionAllows(rule.Exceptions, rel, imp.path) {
			continue
		}
		violations = append(violations, backendBoundaryViolation(rule, rel, imp))
	}
	return violations
}

// storeImportViolationsForRule 保留 store 同 owner 内聚，同时拒绝 registry 未登记的横向和外部依赖。
func storeImportViolationsForRule(rule BackendBoundaryRule, rel string, imports []backendBoundaryImport) []string {
	var violations []string
	for _, imp := range imports {
		if isBackendBoundaryStdlibImport(imp.path) || isSameBackendBoundaryStorePackage(rel, imp.path) || matchesBackendBoundaryStorePolicy(rule.Allow, rel, imp.path) {
			continue
		}
		violations = append(violations, backendBoundaryViolation(rule, rel, imp))
	}
	return violations
}

// matchesBackendBoundaryStorePolicy 对仓库内端口使用前缀语义，对外部包保持既有精确许可。
func matchesBackendBoundaryStorePolicy(policies []BoundaryImportPolicy, rel, imp string) bool {
	for _, policy := range policies {
		if !matchesBackendBoundaryPattern(policy.FilePattern, rel) {
			continue
		}
		prefix := normalizeBackendBoundaryImportPrefix(policy.ImportPrefix)
		if strings.HasPrefix(prefix, "internal/") || strings.HasPrefix(prefix, "cmd/") {
			if matchesBackendBoundaryImportPrefix(imp, prefix) {
				return true
			}
			continue
		}
		if imp == prefix {
			return true
		}
	}
	return false
}

func isBackendBoundaryStdlibImport(imp string) bool {
	if imp == "C" {
		return true
	}
	if strings.Contains(imp, ".") {
		return false
	}
	pkg, err := build.Default.Import(imp, "", build.FindOnly)
	return err == nil && pkg.Goroot
}

func isSameBackendBoundaryStorePackage(rel, imp string) bool {
	dir := filepath.ToSlash(filepath.Dir(rel))
	if dir == "internal/store" || !strings.HasPrefix(dir, "internal/store/") {
		return false
	}
	prefix := backendBoundaryModulePath + "/" + dir
	return imp == prefix || strings.HasPrefix(imp, prefix+"/")
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

// matchesBackendBoundaryPattern 对 validator 接受的有限模式执行确定性路径匹配。
func matchesBackendBoundaryPattern(pattern, rel string) bool {
	pattern = filepath.ToSlash(strings.TrimPrefix(pattern, "./"))
	rel = filepath.ToSlash(strings.TrimPrefix(rel, "./"))
	if matched, handled := matchesBackendBoundaryRecursivePattern(pattern, rel); handled {
		return matched
	}
	if matched, handled := matchesBackendBoundaryDirectPattern(pattern, rel); handled {
		return matched
	}
	return rel == pattern
}

// matchesBackendBoundaryRecursivePattern 求值受支持的递归目录模式。
func matchesBackendBoundaryRecursivePattern(pattern, rel string) (bool, bool) {
	switch {
	case strings.HasSuffix(pattern, "/**/module.go"):
		base := strings.TrimSuffix(pattern, "/**/module.go")
		return strings.HasPrefix(rel, base+"/") && filepath.Base(rel) == "module.go", true
	case strings.HasSuffix(pattern, "/**/*.go"):
		base := strings.TrimSuffix(pattern, "/**/*.go")
		return strings.HasPrefix(rel, base+"/") && strings.HasSuffix(rel, ".go"), true
	case strings.HasSuffix(pattern, "/**"):
		base := strings.TrimSuffix(pattern, "/**")
		return rel == base || strings.HasPrefix(rel, base+"/"), true
	default:
		return false, false
	}
}

func matchesBackendBoundaryDirectPattern(pattern, rel string) (bool, bool) {
	if !strings.HasSuffix(pattern, "/*/main.go") {
		return false, false
	}
	base := strings.TrimSuffix(pattern, "/*/main.go")
	if !strings.HasPrefix(rel, base+"/") || filepath.Base(rel) != "main.go" {
		return false, true
	}
	return strings.Count(strings.TrimPrefix(rel, base+"/"), "/") == 1, true
}

// matchesBackendBoundaryImportPrefix 统一处理仓库内前缀、cmd 前缀和外部完整导入路径。
func matchesBackendBoundaryImportPrefix(imp, prefix string) bool {
	prefix = normalizeBackendBoundaryImportPrefix(prefix)
	if prefix == "frontend-app" {
		return strings.Contains(imp, "/frontend-app") || strings.HasPrefix(imp, "frontend-app")
	}
	if prefix == "cmd" || prefix == "internal" || strings.HasPrefix(prefix, "cmd/") || strings.HasPrefix(prefix, "internal/") {
		prefix = backendBoundaryModulePath + "/" + prefix
	}
	return imp == prefix || strings.HasPrefix(imp, prefix+"/")
}

func normalizeBackendBoundaryImportPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	prefix = strings.ReplaceAll(prefix, "\\", "/")
	prefix = strings.Trim(prefix, "/")
	prefix = strings.TrimPrefix(prefix, backendBoundaryModulePath+"/")
	prefix = path.Clean(prefix)
	if prefix == "." {
		return ""
	}
	return prefix
}

// isCanonicalBackendBoundaryImportPrefix 拒绝空白、路径跳转、重复分隔符和仓库完整模块前缀。
func isCanonicalBackendBoundaryImportPrefix(prefix string) bool {
	if prefix == "" || prefix != strings.TrimSpace(prefix) || strings.Contains(prefix, "\\") {
		return false
	}
	if normalizeBackendBoundaryImportPrefix(prefix) != prefix {
		return false
	}
	for segment := range strings.SplitSeq(prefix, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
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

func backendBoundaryViolation(rule BackendBoundaryRule, rel string, imp backendBoundaryImport) string {
	return fmt.Sprintf("%s:%d:%d imports %s (rule=%s owner=%s reason=%s)", rel, imp.line, imp.column, imp.path, rule.ID, rule.Owner, rule.Reason)
}
