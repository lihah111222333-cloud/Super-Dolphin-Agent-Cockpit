package archtest

import (
	"fmt"
	"go/build"
	"path/filepath"
	"strings"
)

// evaluateBackendBoundaryRule 按已验证的 typed kind 分派规则，未知 kind 仍失败关闭。
func evaluateBackendBoundaryRule(rule BackendBoundaryRule, rel string, imports []backendBoundaryImport) []string {
	switch rule.Kind {
	case BoundaryRuleDenyImports:
		return denyImportViolations(rule, rel, imports)
	case BoundaryRuleAllowInternalImports:
		return allowInternalImportViolations(rule, rel, imports)
	case BoundaryRuleAllowRepositoryImports:
		return allowRepositoryImportViolations(rule, rel, imports)
	case BoundaryRuleAllowExternalImports:
		return allowExternalImportViolations(rule, rel, imports)
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

// denyImportViolations 报告命中 deny 且不在显式例外中的导入。
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

// allowRepositoryImportViolations 对仓库内导入应用精确包白名单，避免祖先前缀意外放行未来实现子包。
func allowRepositoryImportViolations(rule BackendBoundaryRule, rel string, imports []backendBoundaryImport) []string {
	var violations []string
	for _, imp := range imports {
		if !strings.HasPrefix(imp.path, backendBoundaryModulePath+"/") {
			continue
		}
		if !matchesExactBackendBoundaryPolicy(rule.Allow, rel, imp.path) {
			violations = append(violations, backendBoundaryViolation(rule, rel, imp))
		}
	}
	return violations
}

// allowExternalImportViolations 保留标准库和仓库内依赖，只允许 registry 登记的外部 module root。
func allowExternalImportViolations(rule BackendBoundaryRule, rel string, imports []backendBoundaryImport) []string {
	var violations []string
	for _, imp := range imports {
		if isBackendBoundaryStdlibImport(imp.path) || strings.HasPrefix(imp.path, backendBoundaryModulePath+"/") {
			continue
		}
		if !matchesBackendBoundaryPolicy(rule.Allow, rel, imp.path) {
			violations = append(violations, backendBoundaryViolation(rule, rel, imp))
		}
	}
	return violations
}

// matchesExactBackendBoundaryPolicy 对仓库闭合白名单使用精确包语义，不接受子包前缀扩张。
func matchesExactBackendBoundaryPolicy(policies []BoundaryImportPolicy, rel, imp string) bool {
	for _, policy := range policies {
		if !matchesBackendBoundaryPattern(policy.FilePattern, rel) {
			continue
		}
		prefix := normalizeBackendBoundaryImportPrefix(policy.ImportPrefix)
		if strings.HasPrefix(prefix, "internal/") || strings.HasPrefix(prefix, "cmd/") {
			prefix = backendBoundaryModulePath + "/" + prefix
		}
		if imp == prefix {
			return true
		}
	}
	return false
}

// moduleSiblingImportViolationsForRule 拒绝业务 module 导入其他 owner 的具体实现。
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

// scopedImportViolations 仅在文件不属于登记 scope 且无精确例外时报告受限导入。
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

// isBackendBoundaryStdlibImport 判断导入是否来自 Go 标准库。
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

// isSameBackendBoundaryStorePackage 判断 store 导入是否仍位于当前实现 owner。
func isSameBackendBoundaryStorePackage(rel, imp string) bool {
	dir := filepath.ToSlash(filepath.Dir(rel))
	if dir == "internal/store" || !strings.HasPrefix(dir, "internal/store/") {
		return false
	}
	prefix := backendBoundaryModulePath + "/" + dir
	return imp == prefix || strings.HasPrefix(imp, prefix+"/")
}

// matchesBackendBoundaryPolicy 判断文件与导入是否同时命中一条 typed policy。
func matchesBackendBoundaryPolicy(policies []BoundaryImportPolicy, rel, imp string) bool {
	for _, policy := range policies {
		if matchesBackendBoundaryPattern(policy.FilePattern, rel) && matchesBackendBoundaryImportPrefix(imp, policy.ImportPrefix) {
			return true
		}
	}
	return false
}
