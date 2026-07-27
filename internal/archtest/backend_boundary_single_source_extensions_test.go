package archtest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestBackendBoundaryRuleFactsRejectsPolicyHiddenInRegistryFixtureFile(t *testing.T) {
	const source = `package archtest_test
func TestHiddenPolicy(t *testing.T) {
	_ = struct {
		ID string
		Owner string
		Reason string
		Allow []string
	}{ID: "hidden_rule", Owner: "hidden_owner", Reason: "duplicate", Allow: []string{"internal/dto"}}
}`
	rel := "internal/archtest/boundary_registry_test.go"
	node, err := parser.ParseFile(token.NewFileSet(), rel, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	violations := strings.Join(backendBoundaryConsumerFactViolations(rel, node), "\n")
	if !strings.Contains(violations, "declares local backend boundary policy facts") {
		t.Fatalf("registry validation fixtures must not hide local policy tables, got:\n%s", violations)
	}
}

func TestBackendBoundaryRuleFactsRejectsLegacyProviderExternalAllowlist(t *testing.T) {
	const source = `package archtest_test
var providerAllowedExternal = map[string]bool{"example.com/provider": true}
`
	node, err := parser.ParseFile(token.NewFileSet(), "dependency_direction_test.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	violations := strings.Join(backendBoundaryConsumerFactViolations("dependency_direction_test.go", node), "\n")
	if !strings.Contains(violations, "providerAllowedExternal") {
		t.Fatalf("provider external allowlist must be owned by the canonical registry, got:\n%s", violations)
	}
}

func TestBackendBoundaryRuleFactsRejectsRenamedProviderExternalAllowlist(t *testing.T) {
	const source = `package archtest_test
var approvedProviderRoots = map[string]bool{"example.com/provider": true}
`
	node, err := parser.ParseFile(token.NewFileSet(), "dependency_direction_test.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	violations := strings.Join(backendBoundaryConsumerFactViolations("dependency_direction_test.go", node), "\n")
	if !strings.Contains(violations, "approvedProviderRoots") {
		t.Fatalf("renamed provider external allowlist must be rejected by shape, got:\n%s", violations)
	}
}

func TestBackendBoundaryRuleFactsRejectsLegacyContractAllowlist(t *testing.T) {
	const source = `package archtest
func contractAllowedImport(rel string) bool {
	allowed := []string{"internal/contract", "internal/dto/agent"}
	return slices.Contains(allowed, rel)
}
`
	node, err := parser.ParseFile(token.NewFileSet(), "contract_import_boundary_test.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	violations := strings.Join(backendBoundaryConsumerFactViolations("contract_import_boundary_test.go", node), "\n")
	if !strings.Contains(violations, "contractAllowedImport") {
		t.Fatalf("contract allowlist must be owned by the canonical registry, got:\n%s", violations)
	}
}

func TestBackendBoundaryRuleFactsRejectsRenamedContractAllowlist(t *testing.T) {
	const source = `package archtest
func isContractDependencyPermitted(rel string) bool {
	approved := []string{"internal/contract", "internal/dto/agent"}
	return slices.Contains(approved, rel)
}
`
	node, err := parser.ParseFile(token.NewFileSet(), "contract_import_boundary_test.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	violations := strings.Join(backendBoundaryConsumerFactViolations("contract_import_boundary_test.go", node), "\n")
	if !strings.Contains(violations, "isContractDependencyPermitted") {
		t.Fatalf("renamed contract allowlist must be rejected by semantics, got:\n%s", violations)
	}
}

func TestBackendBoundaryRuleFactsRejectsLegacyModuleOuterDependencyEvaluator(t *testing.T) {
	const source = `package archtest_test
func inspectModuleImports(t *testing.T, root string) {
	files := parseImportFiles(t, root, "internal/module/future")
	forbidden := []string{"internal/provider/codexapp", "internal/mcpserver/common", "cmd/agent-runtime"}
	_ = files
	_ = forbidden
}
`
	node, err := parser.ParseFile(token.NewFileSet(), "dependency_direction_wave3_test.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	violations := strings.Join(backendBoundaryConsumerFactViolations("dependency_direction_wave3_test.go", node), "\n")
	if !strings.Contains(violations, "procedural backend dependency evaluator duplicates the canonical registry") {
		t.Fatalf("module outer dependency evaluator must be rejected, got:\n%s", violations)
	}
}

// declaresProceduralModuleOuterBoundary 识别 module 扫描器内重复声明的 provider、MCP server 或 cmd 禁止事实。
func declaresProceduralModuleOuterBoundary(parseTargets, facts map[string]bool) bool {
	if !backendBoundaryFactSetHasPrefix(parseTargets, "internal/module") {
		return false
	}
	return backendBoundaryFactSetHasUnscannedPrefix(facts, parseTargets, "internal/provider") ||
		backendBoundaryFactSetHasUnscannedPrefix(facts, parseTargets, "internal/mcpserver") ||
		backendBoundaryFactSetHasUnscannedPrefix(facts, parseTargets, "cmd")
}

// backendBoundaryFactSetHasUnscannedPrefix 区分并列扫描目标与真正的禁止导入事实。
func backendBoundaryFactSetHasUnscannedPrefix(facts, parseTargets map[string]bool, prefix string) bool {
	for fact := range facts {
		if (fact == prefix || strings.HasPrefix(fact, prefix+"/")) && !parseTargets[fact] {
			return true
		}
	}
	return false
}

// backendBoundaryFactSetHasPrefix 判断事实集合是否包含指定目录或其后代。
func backendBoundaryFactSetHasPrefix(facts map[string]bool, prefix string) bool {
	for fact := range facts {
		if fact == prefix || strings.HasPrefix(fact, prefix+"/") {
			return true
		}
	}
	return false
}

// isExplicitBackendBoundaryValidationLiteral 只放行 registry 测试中的 typed 配置变异夹具。
func isExplicitBackendBoundaryValidationLiteral(rel string, literal *ast.CompositeLit) bool {
	if !backendBoundaryAllowsTestPolicyFixture(rel) {
		return false
	}
	selector, ok := literal.Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok || pkg.Name != "archtest" {
		return false
	}
	return strings.Contains(selector.Sel.Name, "Boundary")
}

// backendBoundarySpecialFunctionViolation 拒绝已知旧 helper 重新成为 registry 外的事实 owner。
func backendBoundarySpecialFunctionViolation(rel string, fn *ast.FuncDecl) string {
	if rel == "internal/archtest/sqlc_boundary_test.go" && strings.HasPrefix(fn.Name.Name, "Test") && !usesCanonicalSQLCBoundaryRule(fn) {
		return rel + ": local SQLC evaluator duplicates the canonical registry"
	}
	switch fn.Name.Name {
	case "defaultBackendBoundaryMatrix", "defaultBoundaryRegistry":
		return rel + ": local default boundary registry duplicates the canonical registry"
	case "contractAllowedImport", "contractImportBoundaryViolation":
		return rel + ": local contract import allowlist helper " + fn.Name.Name + " duplicates the canonical registry"
	case "providerExternalWhitelistViolations", "providerExternalWhitelistViolationsForFiles":
		return rel + ": local provider external allowlist helper " + fn.Name.Name + " duplicates the canonical registry"
	case "moduleOwnerForImportCheck", "moduleSiblingImportViolations", "importedModuleName":
		return rel + ": local module sibling evaluator duplicates the canonical registry"
	case "mcpSidecarFilePatterns", "mcpSidecarImportAllowances":
		return rel + ": local MCP sidecar allowlist helper " + fn.Name.Name + " duplicates the canonical registry"
	default:
		return ""
	}
}

// declaresClosedContractAllowlist 识别改名后仍由 slices.Contains 驱动的 contract/DTO 闭合白名单。
func declaresClosedContractAllowlist(fn *ast.FuncDecl) bool {
	var internalContract, internalDTO, containsCheck bool
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && backendBoundaryCallName(call.Fun) == "Contains" {
			containsCheck = true
		}
		if value, ok := backendBoundaryStringLiteral(node); ok {
			internalContract = internalContract || value == "internal/contract"
			internalDTO = internalDTO || strings.HasPrefix(value, "internal/dto/")
		}
		return true
	})
	return containsCheck && internalContract && internalDTO
}

// backendBoundaryConsumerValueFactViolations 拒绝 package 级数据库或外部 provider 白名单。
func backendBoundaryConsumerValueFactViolations(rel string, item *ast.ValueSpec) []string {
	for _, name := range item.Names {
		if name.Name == "moduleDBImportAllowlist" {
			return []string{rel + ": local moduleDBImportAllowlist duplicates registry exceptions"}
		}
		if name.Name == "providerAllowedExternal" || valueSpecDeclaresExternalImportAllowlist(item) {
			return []string{rel + ": local provider external import allowlist " + name.Name + " duplicates the canonical registry"}
		}
	}
	return nil
}

// valueSpecDeclaresExternalImportAllowlist 按 map 形状识别改名后的外部 module root 白名单。
func valueSpecDeclaresExternalImportAllowlist(item *ast.ValueSpec) bool {
	for _, value := range item.Values {
		literal, ok := value.(*ast.CompositeLit)
		if !ok || !isStringBoolMapType(literal.Type) {
			continue
		}
		for _, element := range literal.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := backendBoundaryStringLiteral(pair.Key)
			if ok && strings.Contains(key, ".") && strings.Contains(key, "/") {
				return true
			}
		}
	}
	return false
}

// isStringBoolMapType 判断 AST 类型是否为旧白名单常用的 map[string]bool。
func isStringBoolMapType(expr ast.Expr) bool {
	mapType, ok := expr.(*ast.MapType)
	if !ok {
		return false
	}
	key, keyOK := mapType.Key.(*ast.Ident)
	value, valueOK := mapType.Value.(*ast.Ident)
	return keyOK && valueOK && key.Name == "string" && value.Name == "bool"
}
