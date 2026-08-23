package mcplspcatalogguard

import "go/ast"

// validateSchedulerPolicyConsumer 校验 candidate input 集合进入 owned gate 路由。
func validateSchedulerPolicyConsumer(relative string, file *ast.File) error {
	policy := namedConsumerFunction(file, "newGatePlanPolicy")
	plan := namedConsumerFunction(file, "applyFileGateRules")
	if policy == nil || !policyHasMcpLSPInputs(policy) || !hasCanonicalWorkloadGateInputSet(file) || plan == nil || hasConditionalSuccessBypass(plan) || !hasTopLevelMethodCall(plan, "policy", "applyOwnedGateRules", "file", "gates") {
		return consumerFlowError(relative, "does not bind MCP-LSP paths to owned gate routing")
	}
	return nil
}

// validateSchedulerRouteConsumer 校验两个 MCP-LSP gate role 的末端路由。
func validateSchedulerRouteConsumer(relative string, file *ast.File) error {
	function := namedConsumerFunction(file, "applyOwnedGateRules")
	if function == nil || function.Body == nil || hasConditionalSuccessBypass(function) {
		return consumerFlowError(relative, "does not define owned MCP-LSP routing")
	}
	if hasExactMcpLSPRoute(function.Body.List) {
		return nil
	}
	return consumerFlowError(relative, "does not route MCP-LSP inputs to both exact gate roles")
}

// hasExactMcpLSPRoute 校验最终分支精确启用两个 role。
func hasExactMcpLSPRoute(statements []ast.Stmt) bool {
	for index, statement := range statements {
		branch, ok := statement.(*ast.IfStmt)
		if !ok || !isMethodCall(branch.Cond, "policy", "mcpLSPWorkloadRelevant", "file") || branch.Else != nil || branch.Body == nil || len(branch.Body.List) != 2 {
			continue
		}
		if index == len(statements)-1 && isTrueMapAssignment(branch.Body.List[0], "gates", "mcp-lsp:catalog") && isTrueMapAssignment(branch.Body.List[1], "gates", "mcp-lsp:idle-quick") {
			return true
		}
	}
	return false
}

// validateSchedulerExecutionConsumer 校验 owned runner map 与 candidate policy 绑定。
func validateSchedulerExecutionConsumer(relative string, file *ast.File) error {
	function := namedConsumerFunction(file, "gateRunners")
	if function == nil || !hasExactOwnedRunnerInstallation(function) || !hasCandidatePolicyBinding(file) {
		return consumerFlowError(relative, "does not install the owned MCP-LSP runner map")
	}
	return nil
}

// hasExactOwnedRunnerInstallation 校验 map copy 是 gateRunners 的最终写入。
func hasExactOwnedRunnerInstallation(function *ast.FuncDecl) bool {
	if function.Body == nil || hasConditionalSuccessBypass(function) || len(function.Body.List) < 2 {
		return false
	}
	copyIndex := len(function.Body.List) - 2
	copyFunction := &ast.FuncDecl{Body: &ast.BlockStmt{List: []ast.Stmt{function.Body.List[copyIndex]}}}
	if !hasTopLevelPackageCall(copyFunction, "maps", "Copy", "runners", "ownedGateRunners", "plan") {
		return false
	}
	result, ok := function.Body.List[copyIndex+1].(*ast.ReturnStmt)
	return ok && len(result.Results) == 1 && isIdentifier(result.Results[0], "runners")
}

func consumerFlowError(relative, detail string) error {
	return &consumerExecutionError{relative: relative, detail: detail}
}

type consumerExecutionError struct {
	relative string
	detail   string
}

// Error 返回 consumer guard 的稳定错误。
func (err *consumerExecutionError) Error() string {
	return "catalog consumer \"" + err.relative + "\" " + err.detail
}

// policyHasMcpLSPInputs 校验 policy 从唯一 workload input owner 派生。
func policyHasMcpLSPInputs(function *ast.FuncDecl) bool {
	if function.Body == nil || len(function.Body.List) != 1 {
		return false
	}
	result, ok := function.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(result.Results) != 1 {
		return false
	}
	literal, ok := result.Results[0].(*ast.CompositeLit)
	if !ok {
		return false
	}
	exact := keyedFieldExpression(literal, "mcpLSPWorkloadExactFiles")
	prefixes := keyedCompositeField(literal, "mcpLSPWorkloadPrefixes")
	call, ok := exact.(*ast.CallExpr)
	return ok && callName(call.Fun) == "workloadGateInputFileSet" && len(call.Args) == 1 &&
		compositeHasStringValues(prefixes, "cmd/mcp-lsp/", "internal/devtools/mcplspguardcore/", "scripts/mcp_lsp_workload_")
}

// hasCanonicalWorkloadGateInputSet 校验 catalog input 动态投影到 file set。
func hasCanonicalWorkloadGateInputSet(file *ast.File) bool {
	function := namedConsumerFunction(file, "workloadGateInputFileSet")
	alias, ok := canonicalImportAlias(file, canonicalCatalogImport, "catalog")
	if function == nil || function.Body == nil || !ok || functionShadowsName(function, alias) || len(function.Body.List) != 3 {
		return false
	}
	if !isWorkloadInputResultInitialization(function.Body.List[0]) {
		return false
	}
	if !isCanonicalWorkloadInputLoop(function.Body.List[1], alias) {
		return false
	}
	result, ok := function.Body.List[2].(*ast.ReturnStmt)
	return ok && len(result.Results) == 1 && isIdentifier(result.Results[0], "result")
}

func isWorkloadInputResultInitialization(statement ast.Stmt) bool {
	assignment, ok := statement.(*ast.AssignStmt)
	return ok && assignmentBindsName(assignment, "result") && assignmentCalls(assignment, "make", func(call *ast.CallExpr) bool { return len(call.Args) == 1 })
}

// isCanonicalWorkloadInputLoop 校验 catalog path 的逐项登记。
func isCanonicalWorkloadInputLoop(statement ast.Stmt, alias string) bool {
	loop, ok := statement.(*ast.RangeStmt)
	if !ok || !isIdentifier(loop.Value, "path") || loop.Body == nil || len(loop.Body.List) != 1 {
		return false
	}
	call, ok := loop.X.(*ast.CallExpr)
	selector, selectorOK := callSelector(call)
	if !ok || !selectorOK || len(call.Args) != 1 {
		return false
	}
	return isWorkloadInputCall(selector, call, alias) && isTrueMapAssignment(loop.Body.List[0], "result", "path")
}

func isWorkloadInputCall(selector *ast.SelectorExpr, call *ast.CallExpr, alias string) bool {
	return isIdentifier(selector.X, alias) && selector.Sel.Name == "WorkloadGateInputPaths" && isIdentifier(call.Args[0], "document")
}

// hasCandidatePolicyBinding 校验 candidate policy 在 plan build 前绑定且未覆盖。
func hasCandidatePolicyBinding(file *ast.File) bool {
	function := namedConsumerFunction(file, "prepareGatePlanForReviewObject")
	if function == nil || function.Body == nil || hasConditionalSuccessBypass(function) {
		return false
	}
	bound := false
	for _, statement := range function.Body.List {
		assignment, ok := statement.(*ast.AssignStmt)
		if !ok {
			continue
		}
		if bindsCandidatePolicy(assignment) {
			bound = true
			continue
		}
		if bound {
			if assignmentBindsName(assignment, "policy") {
				return false
			}
			if buildsPlanWithBoundPolicy(assignment) {
				return true
			}
		}
	}
	return false
}

func bindsCandidatePolicy(assignment *ast.AssignStmt) bool {
	return assignmentBindsName(assignment, "policy") && assignmentCalls(assignment, "bindCandidateWorkloadGateInputs", func(call *ast.CallExpr) bool {
		return hasExactIdentifierArguments(call, "executionRoot", "policy")
	})
}

func buildsPlanWithBoundPolicy(assignment *ast.AssignStmt) bool {
	return assignmentBindsName(assignment, "plan") && assignmentCalls(assignment, "buildGatePlanForRepoAndObject", func(call *ast.CallExpr) bool {
		return len(call.Args) == 4 && isIdentifier(call.Args[0], "executionRoot") && isIdentifier(call.Args[3], "policy")
	})
}

func keyedFieldExpression(literal *ast.CompositeLit, name string) ast.Expr {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if ok && isIdentifier(field.Key, name) {
			return field.Value
		}
	}
	return nil
}

func keyedCompositeField(literal *ast.CompositeLit, name string) *ast.CompositeLit {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok || !isIdentifier(field.Key, name) {
			continue
		}
		value, _ := field.Value.(*ast.CompositeLit)
		return value
	}
	return nil
}

// compositeHasStringValues 校验复合字面量包含全部精确字符串。
func compositeHasStringValues(literal *ast.CompositeLit, values ...string) bool {
	if literal == nil {
		return false
	}
	for _, value := range values {
		found := false
		for _, element := range literal.Elts {
			found = found || isStringLiteral(element, value)
		}
		if !found {
			return false
		}
	}
	return true
}

// hasTopLevelMethodCall 校验顶层方法调用后不存在 MCP role 覆盖。
func hasTopLevelMethodCall(function *ast.FuncDecl, owner, method string, arguments ...string) bool {
	for index, statement := range function.Body.List {
		expression, ok := statement.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, callOK := expression.X.(*ast.CallExpr)
		selector, selectorOK := callSelector(call)
		if callOK && selectorOK && selector.Sel.Name == method && isIdentifier(selector.X, owner) && hasExactIdentifierArguments(call, arguments...) {
			return !statementsAfterOverrideMcpRoles(function.Body.List[index+1:])
		}
	}
	return false
}

func statementsAfterOverrideMcpRoles(statements []ast.Stmt) bool {
	override := false
	for _, statement := range statements {
		ast.Inspect(statement, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if ok && (isStringLiteral(literal, "mcp-lsp:catalog") || isStringLiteral(literal, "mcp-lsp:idle-quick")) {
				override = true
			}
			return !override
		})
	}
	return override
}

func isMethodCall(expression ast.Expr, owner, method string, arguments ...string) bool {
	call, ok := expression.(*ast.CallExpr)
	selector, selectorOK := callSelector(call)
	return ok && selectorOK && selector.Sel.Name == method && isIdentifier(selector.X, owner) && hasExactIdentifierArguments(call, arguments...)
}

// isTrueMapAssignment 校验 map key 被精确赋 true。
func isTrueMapAssignment(statement ast.Stmt, owner, key string) bool {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 || !isIdentifier(assignment.Rhs[0], "true") {
		return false
	}
	index, ok := assignment.Lhs[0].(*ast.IndexExpr)
	if !ok {
		return false
	}
	keyMatches := isStringLiteral(index.Index, key)
	if key == "path" {
		keyMatches = isIdentifier(index.Index, key)
	}
	return isIdentifier(index.X, owner) && keyMatches
}

// hasTopLevelPackageCall 校验顶层包调用的参数数据流。
func hasTopLevelPackageCall(function *ast.FuncDecl, owner, method, firstArgument, nestedCall, nestedArgument string) bool {
	for _, statement := range function.Body.List {
		if matchesTopLevelPackageCall(statement, owner, method, firstArgument, nestedCall, nestedArgument) {
			return true
		}
	}
	return false
}

func matchesTopLevelPackageCall(statement ast.Stmt, owner, method, firstArgument, nestedCall, nestedArgument string) bool {
	expression, ok := statement.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := expression.X.(*ast.CallExpr)
	selector, selectorOK := callSelector(call)
	if !ok || !selectorOK || len(call.Args) != 2 {
		return false
	}
	if !isIdentifier(selector.X, owner) || selector.Sel.Name != method || !isIdentifier(call.Args[0], firstArgument) {
		return false
	}
	nested, ok := call.Args[1].(*ast.CallExpr)
	return ok && callName(nested.Fun) == nestedCall && hasExactIdentifierArguments(nested, nestedArgument)
}
