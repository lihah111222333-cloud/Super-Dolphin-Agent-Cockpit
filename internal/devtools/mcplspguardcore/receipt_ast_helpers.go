package mcplspcatalogguard

import "go/ast"

const canonicalTrustedLauncherImport = "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/trustedlauncher"

func hasLiveCatalogGuardBuildFlow(file *ast.File) bool {
	function := namedConsumerFunction(file, "runMcpLSPCatalogGuard")
	alias, ok := canonicalImportAlias(file, canonicalTrustedLauncherImport, "trustedlauncher")
	if !validCatalogBuildFunction(function, alias, ok) {
		return false
	}
	statements := function.Body.List
	build := statementIndexAfter(statements, 0, func(statement ast.Stmt) bool {
		assignment, ok := statement.(*ast.AssignStmt)
		return ok && assignmentBindsName(assignment, "result") && assignmentBindsName(assignment, "err") && assignmentCallsSelector(assignment, alias, "Build", isCandidateLauncherBuild)
	})
	check := statementIndexAfter(statements, build+1, func(statement ast.Stmt) bool { return isCheckedErrorReturn(statement, "err") })
	execute := statementIndexAfter(statements, check+1, isCandidateGuardExecutionReturn)
	if build < 0 || execute != check+1 || execute != len(statements)-1 {
		return false
	}
	return check == build+1 || check == build+2 && isExactCallStatement(statements[build+1], "cancel")
}

func validCatalogBuildFunction(function *ast.FuncDecl, alias string, imported bool) bool {
	if function == nil || function.Body == nil || !imported {
		return false
	}
	return !functionShadowsName(function, alias) && !hasConditionalSuccessBypass(function)
}

func isCandidateLauncherBuild(call *ast.CallExpr) bool {
	if len(call.Args) != 2 || !isIdentifier(call.Args[0], "ctx") {
		return false
	}
	literal, ok := call.Args[1].(*ast.CompositeLit)
	if !ok {
		return false
	}
	return compositeFieldMatches(literal, "RepositoryRoot", isGateExecutionRoot) &&
		compositeFieldMatches(literal, "Tree", isCandidateTreeExpr) &&
		compositeFieldMatches(literal, "InstallRoot", func(expression ast.Expr) bool { return isIdentifier(expression, "installRoot") })
}

func compositeFieldMatches(literal *ast.CompositeLit, name string, match func(ast.Expr) bool) bool {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if ok && isIdentifier(field.Key, name) && match(field.Value) {
			return true
		}
	}
	return false
}

func isGateExecutionRoot(expression ast.Expr) bool {
	call, ok := expression.(*ast.CallExpr)
	return ok && callName(call.Fun) == "gateExecutionDir" && len(call.Args) == 2 && isIdentifier(call.Args[0], "plan") && isStringLiteral(call.Args[1], "")
}

func isCandidateGuardExecutionReturn(statement ast.Stmt) bool {
	result, ok := statement.(*ast.ReturnStmt)
	if !ok || len(result.Results) != 1 {
		return false
	}
	call, ok := result.Results[0].(*ast.CallExpr)
	if !ok || callName(call.Fun) != "runCommandWithEnvironment" || len(call.Args) != 9 {
		return false
	}
	return catalogGuardExecutionPrefixMatches(call.Args[:5]) && catalogGuardExecutionAuthorityMatches(call.Args[5:])
}

func catalogGuardExecutionPrefixMatches(arguments []ast.Expr) bool {
	return isGateExecutionRoot(arguments[0]) && isSelector(arguments[1], "result", "BinaryPath") &&
		isClosedCatalogGuardEnvironment(arguments[2]) && isStringLiteral(arguments[3], "worker") &&
		isStringLiteral(arguments[4], "mcp-lsp-catalog-guard")
}

func catalogGuardExecutionAuthorityMatches(arguments []ast.Expr) bool {
	return isStringLiteral(arguments[0], "--repository") && isGateExecutionRoot(arguments[1]) &&
		isStringLiteral(arguments[2], "--tree") && isCandidateTreeExpr(arguments[3])
}

func isClosedCatalogGuardEnvironment(expression ast.Expr) bool {
	literal, ok := expression.(*ast.CompositeLit)
	return ok && len(literal.Elts) == 1 && isStringLiteral(literal.Elts[0], "LC_ALL=C")
}

func hasLiveReleaseAuthorityFlow(file *ast.File) bool {
	function := namedConsumerFunction(file, "runMcpLSPQuickReleaseAuthority")
	alias, ok := canonicalImportAlias(file, canonicalCatalogImport, "catalog")
	if !validCatalogBuildFunction(function, alias, ok) {
		return false
	}
	statements := function.Body.List
	for index, statement := range statements {
		if _, ok := statement.(*ast.ReturnStmt); ok && index != len(statements)-1 {
			return false
		}
	}
	load := statementIndexAfter(statements, 0, func(statement ast.Stmt) bool { return isCandidateCatalogLoad(statement, alias) })
	loadCheck := statementIndexAfter(statements, load+1, func(statement ast.Stmt) bool { return isCheckedErrorReturn(statement, "err") })
	find := statementIndexAfter(statements, loadCheck+1, isQuickWorkloadFind)
	findCheck := statementIndexAfter(statements, find+1, func(statement ast.Stmt) bool { return isCheckedErrorReturn(statement, "err") })
	authority := statementIndexAfter(statements, findCheck+1, isQuickAuthorityReturn)
	if !releaseAuthorityOrder(load, loadCheck, find, findCheck, authority) {
		return false
	}
	return authority == len(statements)-1 && hasCanonicalAuthorityHelper(file, alias)
}

func releaseAuthorityOrder(load, loadCheck, find, findCheck, authority int) bool {
	return load >= 0 && loadCheck > load && find > loadCheck && findCheck > find && authority > findCheck
}

func isCandidateCatalogLoad(statement ast.Stmt, alias string) bool {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) != 2 || len(assignment.Rhs) != 1 {
		return false
	}
	if !isIdentifier(assignment.Lhs[0], "document") || !isIdentifier(assignment.Lhs[1], "err") {
		return false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	selector, selectorOK := callSelector(call)
	if !ok || !selectorOK {
		return false
	}
	if len(call.Args) != 2 {
		return false
	}
	return isCandidateCatalogLoadCall(selector, call, alias)
}

func isCandidateCatalogLoadCall(selector *ast.SelectorExpr, call *ast.CallExpr, alias string) bool {
	return isIdentifier(selector.X, alias) && selector.Sel.Name == "LoadAt" &&
		isGateExecutionRoot(call.Args[0]) && isCandidateTreeExpr(call.Args[1])
}

func isQuickWorkloadFind(statement ast.Stmt) bool {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) != 2 || !isIdentifier(assignment.Lhs[0], "workload") || !isIdentifier(assignment.Lhs[1], "err") || len(assignment.Rhs) != 1 {
		return false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	if !ok || callName(call.Fun) != "Find" || len(call.Args) != 1 {
		return false
	}
	_, literal := call.Args[0].(*ast.BasicLit)
	return literal
}

func isQuickAuthorityReturn(statement ast.Stmt) bool {
	result, ok := statement.(*ast.ReturnStmt)
	if !ok || len(result.Results) != 1 {
		return false
	}
	call, ok := result.Results[0].(*ast.CallExpr)
	return ok && callName(call.Fun) == "requireMcpLSPQuickReleaseAuthority" && len(call.Args) == 1 && isIdentifier(call.Args[0], "workload")
}

func hasCanonicalAuthorityHelper(file *ast.File, alias string) bool {
	function := namedConsumerFunction(file, "requireMcpLSPQuickReleaseAuthority")
	if !hasCanonicalAuthorityHelperShape(function, alias) {
		return false
	}
	result, ok := function.Body.List[1].(*ast.ReturnStmt)
	if !ok || len(result.Results) != 1 {
		return false
	}
	call, ok := result.Results[0].(*ast.CallExpr)
	selector, selectorOK := callSelector(call)
	return ok && selectorOK && isIdentifier(selector.X, alias) && selector.Sel.Name == "RequireRemoteCompletionAuthority" &&
		len(call.Args) == 1 && isIdentifier(call.Args[0], "workload")
}

func hasCanonicalAuthorityHelperShape(function *ast.FuncDecl, alias string) bool {
	if function == nil || function.Body == nil || len(function.Body.List) != 2 {
		return false
	}
	return !functionShadowsName(function, alias) && !hasConditionalSuccessBypass(function) && isIdleQuickRoleCheck(function.Body.List[0], alias)
}

func isIdleQuickRoleCheck(statement ast.Stmt, alias string) bool {
	branch, ok := statement.(*ast.IfStmt)
	if !ok || branch.Init == nil {
		return false
	}
	if !exactErrorBranchBody(branch) {
		return false
	}
	call, ok := idleQuickRoleCall(branch.Init)
	if !ok {
		return false
	}
	selector, selectorOK := callSelector(call)
	if !selectorOK || !isIdleQuickRoleSelector(selector, alias) || len(call.Args) != 1 {
		return false
	}
	return isExactIdleQuickRoleCheck(branch, call)
}

func exactErrorBranchBody(branch *ast.IfStmt) bool {
	return branch.Else == nil && branch.Body != nil && len(branch.Body.List) == 1
}

func isExactIdleQuickRoleCheck(branch *ast.IfStmt, call *ast.CallExpr) bool {
	return isIdentifier(call.Args[0], "workload") && isErrNotNil(branch.Cond) &&
		returnsIdentifier(branch.Body.List[0], "err")
}

func isIdleQuickRoleSelector(selector *ast.SelectorExpr, alias string) bool {
	return isIdentifier(selector.X, alias) && selector.Sel.Name == "RequireIdleQuickReleaseRole"
}

func idleQuickRoleCall(statement ast.Stmt) (*ast.CallExpr, bool) {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) != 1 || !isIdentifier(assignment.Lhs[0], "err") || len(assignment.Rhs) != 1 {
		return nil, false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	return call, ok
}

func returnsIdentifier(statement ast.Stmt, name string) bool {
	result, ok := statement.(*ast.ReturnStmt)
	return ok && len(result.Results) == 1 && isIdentifier(result.Results[0], name)
}

func isCheckedErrorReturn(statement ast.Stmt, name string) bool {
	branch, ok := statement.(*ast.IfStmt)
	if !ok || branch.Init != nil || branch.Else != nil || branch.Body == nil || len(branch.Body.List) != 1 || !expressionIsNonNilCheck(branch.Cond, name) {
		return false
	}
	result, ok := branch.Body.List[0].(*ast.ReturnStmt)
	return ok && len(result.Results) > 0 && expressionContainsIdentifier(result.Results[len(result.Results)-1], name)
}

func isExactCallStatement(statement ast.Stmt, name string) bool {
	expression, ok := statement.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := expression.X.(*ast.CallExpr)
	return ok && callName(call.Fun) == name && len(call.Args) == 0
}

func expressionContainsIdentifier(expression ast.Expr, name string) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		if isIdentifierExpression, ok := node.(*ast.Ident); ok && isIdentifierExpression.Name == name {
			found = true
		}
		return !found
	})
	return found
}

func expressionIsNonNilCheck(expression ast.Expr, name string) bool {
	comparison, ok := expression.(*ast.BinaryExpr)
	return ok && comparison.Op.String() == "!=" && isIdentifier(comparison.X, name) && isIdentifier(comparison.Y, "nil")
}

// namedConsumerFunction 返回生产 consumer 的顶层函数，拒绝嵌套死分支自证。
func namedConsumerFunction(file *ast.File, name string) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name != nil && function.Name.Name == name {
			return function
		}
	}
	return nil
}

func isCandidateTreeExpr(expression ast.Expr) bool {
	candidate, ok := expression.(*ast.SelectorExpr)
	if !ok || candidate.Sel == nil || candidate.Sel.Name != "CandidateTree" {
		return false
	}
	review, ok := candidate.X.(*ast.SelectorExpr)
	if !ok || review.Sel == nil || review.Sel.Name != "ReviewObject" {
		return false
	}
	plan, ok := review.X.(*ast.Ident)
	return ok && plan.Name == "plan"
}
