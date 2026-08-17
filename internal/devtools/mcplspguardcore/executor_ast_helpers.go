package mcplspcatalogguard

import (
	"go/ast"
	"go/token"
	"strconv"
)

func isStringLiteral(expression ast.Expr, want string) bool {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(literal.Value)
	return err == nil && value == want
}

// hasLiveExecutorMapping 要求 catalog target case 返回同一 live guard program。
func hasLiveExecutorMapping(file *ast.File) bool {
	function := namedConsumerFunction(file, "splitSourceGoGuardExecutorProgram")
	if function == nil || function.Body == nil || hasConditionalSuccessBypass(function) {
		return false
	}
	for _, statement := range function.Body.List {
		switchStatement, ok := statement.(*ast.SwitchStmt)
		if !ok || switchStatement.Body == nil {
			continue
		}
		if switchReturnsMcpLSPProgram(switchStatement) {
			return true
		}
	}
	return false
}

func switchReturnsMcpLSPProgram(statement *ast.SwitchStmt) bool {
	for _, clauseStatement := range statement.Body.List {
		clause, ok := clauseStatement.(*ast.CaseClause)
		if ok && exactCaseIdentifier(clause, "GoGuardTargetMcpLSPWorkloadCatalog") && caseReturnsMappedProgram(clause) {
			return true
		}
	}
	return false
}

func exactCaseIdentifier(clause *ast.CaseClause, name string) bool {
	if len(clause.List) != 1 {
		return false
	}
	identifier, ok := clause.List[0].(*ast.Ident)
	return ok && identifier.Name == name
}

// caseReturnsMappedProgram 要求目标 case 的 commandProgram 结果由同名变量返回。
func caseReturnsMappedProgram(clause *ast.CaseClause) bool {
	if len(clause.Body) != 2 {
		return false
	}
	assigned, _ := mappedProgramStatementFacts(clause.Body[0])
	_, returned := mappedProgramStatementFacts(clause.Body[1])
	return assigned && returned
}

// mappedProgramStatementFacts 提取同一 case 内 program 赋值与返回事实。
func mappedProgramStatementFacts(statement ast.Stmt) (bool, bool) {
	switch value := statement.(type) {
	case *ast.AssignStmt:
		if len(value.Lhs) != 1 || !isIdentifier(value.Lhs[0], "program") {
			return false, false
		}
		return assignmentContainsCatalogCommand(value), false
	case *ast.ReturnStmt:
		return false, len(value.Results) == 2 && isIdentifier(value.Results[0], "program") && isBooleanLiteral(value.Results[1], true)
	default:
		return false, false
	}
}

// assignmentContainsCatalogCommand 校验 program assignment 内的精确 guard argv。
func assignmentContainsCatalogCommand(statement *ast.AssignStmt) bool {
	found := false
	for _, expression := range statement.Rhs {
		ast.Inspect(expression, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if ok && callName(call.Fun) == "commandProgram" && len(call.Args) == 1 && exactCatalogWorkerSlice(call.Args[0]) {
				found = true
				return false
			}
			return !found
		})
	}
	return found
}

func exactCatalogWorkerSlice(expression ast.Expr) bool {
	literal, ok := expression.(*ast.CompositeLit)
	return ok && len(literal.Elts) == 3 && isIdentifier(literal.Elts[0], "ExecutorSelfCommandName") &&
		isStringLiteral(literal.Elts[1], "worker") && isStringLiteral(literal.Elts[2], "mcp-lsp-catalog-guard")
}

// hasLiveExecutorTreeCall 校验 executor range 中 tree binder 的读取与写回。
func hasLiveExecutorTreeCall(file *ast.File) bool {
	function := namedConsumerFunction(file, "resolveExecutorStepsWithCandidateTree")
	if function == nil || function.Body == nil || hasConditionalSuccessBypass(function) {
		return false
	}
	for index, statement := range function.Body.List {
		loop, ok := statement.(*ast.RangeStmt)
		if ok && index == len(function.Body.List)-2 && isIdentifier(loop.X, "steps") && rangeBindsCatalogTree(loop) && returnsStepsNil(function.Body.List[index+1]) {
			return true
		}
	}
	return false
}

func returnsStepsNil(statement ast.Stmt) bool {
	result, ok := statement.(*ast.ReturnStmt)
	return ok && len(result.Results) == 2 && isIdentifier(result.Results[0], "steps") && isIdentifier(result.Results[1], "nil")
}

// rangeBindsCatalogTree 校验同一 steps range 内的 bind 与 argv 写回。
func rangeBindsCatalogTree(loop *ast.RangeStmt) bool {
	if loop.Body == nil || len(loop.Body.List) != 3 {
		return false
	}
	assignment, ok := loop.Body.List[0].(*ast.AssignStmt)
	if !ok || !assignmentBindsName(assignment, "err") || !assignmentCalls(assignment, "injectMcpLSPCandidateTreeAt", exactExecutorTreeCall) {
		return false
	}
	if !isCheckedErrorReturn(loop.Body.List[1], "err") {
		return false
	}
	store, ok := loop.Body.List[2].(*ast.AssignStmt)
	return ok && assignsStepArgv(store)
}

// exactExecutorTreeCall 要求 steps[index].argv 与 candidateTree 精确有序传入。
func exactExecutorTreeCall(call *ast.CallExpr) bool {
	if len(call.Args) != 3 || !isIdentifier(call.Args[1], "candidateTree") || !isIdentifier(call.Args[2], "sourceCopy") {
		return false
	}
	selector, ok := call.Args[0].(*ast.SelectorExpr)
	return ok && selector.Sel != nil && selector.Sel.Name == "argv" && isIndexedIdentifier(selector.X, "steps", "index")
}

// assignsStepArgv 要求 binder 返回 argv 写回相同 steps[index]。
func assignsStepArgv(statement *ast.AssignStmt) bool {
	if len(statement.Lhs) != 1 || len(statement.Rhs) != 1 || !isIdentifier(statement.Rhs[0], "argv") {
		return false
	}
	selector, ok := statement.Lhs[0].(*ast.SelectorExpr)
	return ok && selector.Sel != nil && selector.Sel.Name == "argv" && isIndexedIdentifier(selector.X, "steps", "index")
}

func isIndexedIdentifier(expression ast.Expr, collection, index string) bool {
	indexed, ok := expression.(*ast.IndexExpr)
	return ok && isIdentifier(indexed.X, collection) && isIdentifier(indexed.Index, index)
}

func hasLiveExecutorTreeHelper(file *ast.File) bool {
	guard := namedConsumerFunction(file, "injectMcpLSPGuardTree")
	makeTree := namedConsumerFunction(file, "injectMcpLSPMakeTree")
	return hasGuardTreeAppendReturn(guard) && hasMakeTreeAppendReturn(makeTree)
}

func hasGuardTreeAppendReturn(function *ast.FuncDecl) bool {
	return hasExactAppendReturn(function, func(arguments []ast.Expr) bool {
		return len(arguments) == 3 && isSlicesCloneArgv(arguments[0]) && isStringLiteral(arguments[1], "--tree") && isIdentifier(arguments[2], "candidateTree")
	})
}

func hasMakeTreeAppendReturn(function *ast.FuncDecl) bool {
	return hasExactAppendReturn(function, func(arguments []ast.Expr) bool {
		return len(arguments) == 2 && isSlicesCloneArgv(arguments[0]) && isStringConcatIdentifier(arguments[1], "MCP_LSP_CANDIDATE_TREE=", "candidateTree")
	})
}

// hasExactAppendReturn 只接受函数顶层 append return，拒绝死分支 helper。
func hasExactAppendReturn(function *ast.FuncDecl, validate func([]ast.Expr) bool) bool {
	if function == nil || function.Body == nil || hasConditionalSuccessBypass(function) {
		return false
	}
	for _, statement := range function.Body.List {
		if validAppendReturn(statement, validate) {
			return true
		}
	}
	return false
}

// validAppendReturn 校验顶层 append 返回及可选 nil error。
func validAppendReturn(statement ast.Stmt, validate func([]ast.Expr) bool) bool {
	result, ok := statement.(*ast.ReturnStmt)
	if !ok || len(result.Results) == 0 || (len(result.Results) > 1 && !isIdentifier(result.Results[1], "nil")) {
		return false
	}
	call, ok := result.Results[0].(*ast.CallExpr)
	return ok && callName(call.Fun) == "append" && validate(call.Args)
}

// isSlicesCloneArgv 校验 append 的基底来自 slices.Clone(argv)。
func isSlicesCloneArgv(expression ast.Expr) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 || !isIdentifier(call.Args[0], "argv") {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel != nil && selector.Sel.Name == "Clone" && isIdentifier(selector.X, "slices")
}

func isStringConcatIdentifier(expression ast.Expr, prefix, identifier string) bool {
	binary, ok := expression.(*ast.BinaryExpr)
	return ok && binary.Op == token.ADD && isStringLiteral(binary.X, prefix) && isIdentifier(binary.Y, identifier)
}

func isIdentifier(expression ast.Expr, name string) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == name
}

func isBooleanLiteral(expression ast.Expr, want bool) bool {
	return isIdentifier(expression, strconv.FormatBool(want))
}
