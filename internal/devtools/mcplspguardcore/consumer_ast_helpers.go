package mcplspcatalogguard

import (
	"go/ast"
	"go/token"
	"strconv"
)

const canonicalCatalogImport = "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
const canonicalGuardImport = "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/mcplspguardcore"

// isIdentifier is the shared AST identity predicate for live catalog consumers.
func isIdentifier(expression ast.Expr, name string) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == name
}

// isStringLiteral recognizes one exact, decoded string literal in a fixture AST.
func isStringLiteral(expression ast.Expr, want string) bool {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(literal.Value)
	return err == nil && value == want
}

// hasLiveRunnerCatalogFlow 要求 runner 顶层加载路径把 catalog 与 workload 查询绑定到
// 同一组显式 repo/tree 参数。
func hasLiveRunnerCatalogFlow(file *ast.File) bool {
	function := namedConsumerFunction(file, "loadRunnerWorkload")
	alias, ok := canonicalImportAlias(file, canonicalCatalogImport, "catalog")
	if function == nil || function.Body == nil || !ok || functionShadowsName(function, alias) || hasConditionalSuccessBypass(function) {
		return false
	}
	return hasTopLevelSelectorAssignment(function, alias, "LoadAt", "repoRoot", "tree") &&
		hasTopLevelSelectorAssignment(function, "document", "Find", "id") &&
		hasRunnerWorkloadReturn(function) &&
		hasRunnerExecutionFlow(file)
}

func hasLiveLocalHostExecutionFlow(file *ast.File) bool {
	function := namedConsumerFunction(file, "executeRunnerWorkload")
	if function == nil || function.Body == nil || hasConditionalSuccessBypass(function) || hasRawCatalogCommandExecution(file) {
		return false
	}
	build := statementIndexAfter(function.Body.List, 0, isLocalHostCommandBinding)
	execute := statementIndexAfter(function.Body.List, build+1, isLocalHostCommandReturn)
	return build >= 0 && execute > build
}

func hasLiveGuardWorkerBinding(file *ast.File) bool {
	function := namedConsumerFunction(file, "runMcpLSPCatalogGuardWorker")
	alias, ok := canonicalImportAlias(file, canonicalGuardImport, "mcplspcatalogguard")
	if function == nil || function.Body == nil || !ok || functionShadowsName(function, alias) || len(function.Body.List) != 2 {
		return false
	}
	return isGuardWorkerRunBranch(function.Body.List[0], alias) && isGuardWorkerSuccessReturn(function.Body.List[1])
}

func isGuardWorkerRunBranch(statement ast.Stmt, alias string) bool {
	branch, ok := statement.(*ast.IfStmt)
	if !ok || branch.Else != nil || branch.Body == nil || len(branch.Body.List) != 1 {
		return false
	}
	call, ok := guardWorkerRunAssignment(branch.Init)
	if !ok {
		return false
	}
	return isCanonicalGuardRunCall(call, alias) && isErrNotNil(branch.Cond) && isGuardWorkerFailureReturn(branch.Body.List[0])
}

func guardWorkerRunAssignment(statement ast.Stmt) (*ast.CallExpr, bool) {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || assignment.Tok.String() != ":=" || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return nil, false
	}
	if !isIdentifier(assignment.Lhs[0], "err") {
		return nil, false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	return call, ok
}

func isCanonicalGuardRunCall(call *ast.CallExpr, alias string) bool {
	selector, ok := callSelector(call)
	return ok && selector.Sel.Name == "Run" && isIdentifier(selector.X, alias) &&
		len(call.Args) == 2 && isIdentifier(call.Args[0], "args") && isIdentifier(call.Args[1], "stdout")
}

func isErrNotNil(expression ast.Expr) bool {
	condition, ok := expression.(*ast.BinaryExpr)
	return ok && condition.Op.String() == "!=" && isIdentifier(condition.X, "err") && isIdentifier(condition.Y, "nil")
}

func isGuardWorkerFailureReturn(statement ast.Stmt) bool {
	result, ok := statement.(*ast.ReturnStmt)
	if !ok || len(result.Results) != 1 {
		return false
	}
	call, ok := result.Results[0].(*ast.CallExpr)
	return ok && callName(call.Fun) == "workerBuiltinErrorExitCode" && hasExactIdentifierArguments(call, "stderr", "err")
}

func isGuardWorkerSuccessReturn(statement ast.Stmt) bool {
	return isGuardWorkerReturn(statement, "ExitOK")
}

func isGuardWorkerReturn(statement ast.Stmt, member string, arguments ...string) bool {
	conversion, ok := guardWorkerReturnConversion(statement)
	if !ok {
		return false
	}
	if len(arguments) == 0 {
		return isSelector(conversion.Args[0], "gatecontract", member)
	}
	return isGateContractCall(conversion.Args[0], member, arguments)
}

func guardWorkerReturnConversion(statement ast.Stmt) (*ast.CallExpr, bool) {
	result, ok := statement.(*ast.ReturnStmt)
	if !ok || len(result.Results) != 1 {
		return nil, false
	}
	conversion, ok := result.Results[0].(*ast.CallExpr)
	if !ok || callName(conversion.Fun) != "int" || len(conversion.Args) != 1 {
		return nil, false
	}
	return conversion, true
}

func isGateContractCall(expression ast.Expr, member string, arguments []string) bool {
	call, ok := expression.(*ast.CallExpr)
	selector, selectorOK := callSelector(call)
	return ok && selectorOK && selector.Sel.Name == member && isIdentifier(selector.X, "gatecontract") && hasExactIdentifierArguments(call, arguments...)
}

func callSelector(call *ast.CallExpr) (*ast.SelectorExpr, bool) {
	if call == nil {
		return nil, false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return selector, ok && selector.Sel != nil
}

func isLocalHostCommandBinding(statement ast.Stmt) bool {
	assignment, ok := statement.(*ast.AssignStmt)
	return ok && assignmentBindsName(assignment, "hostCommand") && assignmentCalls(assignment, "LocalHostTestCommand", func(call *ast.CallExpr) bool {
		return hasExactIdentifierArguments(call, "canonicalWorkload")
	})
}

func isLocalHostCommandReturn(statement ast.Stmt) bool {
	result, ok := statement.(*ast.ReturnStmt)
	return ok && returnCalls(result, "executeLocalHostCommand", func(call *ast.CallExpr) bool {
		return len(call.Args) == 5 && isIdentifier(call.Args[0], "repoRoot") && isSelector(call.Args[1], "canonicalWorkload", "ID") &&
			isIdentifier(call.Args[2], "hostCommand") && isIdentifier(call.Args[3], "head") && isSelector(call.Args[4], "options", "tree")
	})
}

func hasRawCatalogCommandExecution(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || callName(call.Fun) != "CommandContext" {
			return true
		}
		for _, argument := range call.Args {
			ast.Inspect(argument, func(nested ast.Node) bool {
				selector, ok := nested.(*ast.SelectorExpr)
				if ok && selector.Sel != nil && selector.Sel.Name == "Command" {
					found = true
				}
				return !found
			})
		}
		return !found
	})
	return found
}

func statementIndexAfter(statements []ast.Stmt, start int, match func(ast.Stmt) bool) int {
	if start < 0 {
		return -1
	}
	for index := start; index < len(statements); index++ {
		if match(statements[index]) {
			return index
		}
	}
	return -1
}

func assignmentCallsSelector(statement *ast.AssignStmt, owner, name string, validate func(*ast.CallExpr) bool) bool {
	for _, expression := range statement.Rhs {
		call, ok := expression.(*ast.CallExpr)
		selector, selectorOK := callSelector(call)
		if ok && selectorOK && isIdentifier(selector.X, owner) && selector.Sel.Name == name && validate(call) {
			return true
		}
	}
	return false
}

func assignmentBindsName(assignment *ast.AssignStmt, name string) bool {
	for _, expression := range assignment.Lhs {
		if isIdentifier(expression, name) {
			return true
		}
	}
	return false
}

func isSelector(expression ast.Expr, owner, name string) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	return ok && selector.Sel != nil && selector.Sel.Name == name && isIdentifier(selector.X, owner)
}

// hasRunnerExecutionFlow 校验 run 顶层 load、validate 与最终 execute 数据流。
func hasRunnerExecutionFlow(file *ast.File) bool {
	function := namedConsumerFunction(file, "run")
	if function == nil || function.Body == nil || hasConditionalSuccessBypass(function) {
		return false
	}
	loaded, validated, executed := false, false, false
	for _, statement := range function.Body.List {
		loadFact, validateFact, executeFact := runnerExecutionStatementFacts(statement)
		loaded, validated, executed = loaded || loadFact, validated || validateFact, executed || executeFact
	}
	return loaded && validated && executed
}

// hasConditionalSuccessBypass 拒绝可在权威调用后改写结果或提前成功的控制流。
func hasConditionalSuccessBypass(function *ast.FuncDecl) bool {
	if function == nil || function.Body == nil {
		return true
	}
	if hasNamedResults(function) || containsDefer(function.Body) {
		return true
	}
	return containsEarlySuccessReturn(function.Body.List)
}

func hasNamedResults(function *ast.FuncDecl) bool {
	if function.Type.Results == nil {
		return false
	}
	for _, field := range function.Type.Results.List {
		if len(field.Names) != 0 {
			return true
		}
	}
	return false
}

func containsDefer(body *ast.BlockStmt) bool {
	deferred := false
	ast.Inspect(body, func(node ast.Node) bool {
		if _, ok := node.(*ast.DeferStmt); ok {
			deferred = true
		}
		return !deferred
	})
	return deferred
}

func containsEarlySuccessReturn(statements []ast.Stmt) bool {
	for index, statement := range statements {
		if index == len(statements)-1 {
			if _, ok := statement.(*ast.ReturnStmt); ok {
				continue
			}
		}
		found := false
		ast.Inspect(statement, func(node ast.Node) bool {
			result, ok := node.(*ast.ReturnStmt)
			if !ok {
				return true
			}
			found = len(result.Results) == 0 || isIdentifier(result.Results[len(result.Results)-1], "nil")
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

// runnerExecutionStatementFacts 从一个顶层 statement 提取 runner 调用链事实。
func runnerExecutionStatementFacts(statement ast.Stmt) (bool, bool, bool) {
	switch value := statement.(type) {
	case *ast.AssignStmt:
		return assignmentCalls(value, "loadRunnerWorkload", hasRunnerLoadArguments), false, false
	case *ast.IfStmt:
		valid := ifInitCalls(value, "validateRunnerWorkload", func(call *ast.CallExpr) bool {
			return hasExactIdentifierArguments(call, "workload")
		}) && isErrNotNil(value.Cond) && value.Body != nil && len(value.Body.List) == 1 && returnsIdentifier(value.Body.List[0], "err")
		return false, valid, false
	case *ast.ReturnStmt:
		executed := returnCalls(value, "executeRunnerWorkload", func(call *ast.CallExpr) bool {
			return hasIdentifierArgument(call, "workload") && hasIdentifierArgument(call, "document")
		})
		return false, false, executed
	default:
		return false, false, false
	}
}

func assignmentCalls(statement *ast.AssignStmt, name string, validate func(*ast.CallExpr) bool) bool {
	for _, expression := range statement.Rhs {
		call, ok := expression.(*ast.CallExpr)
		if ok && callName(call.Fun) == name && validate(call) {
			return true
		}
	}
	return false
}

func ifInitCalls(statement *ast.IfStmt, name string, validate func(*ast.CallExpr) bool) bool {
	assignment, ok := statement.Init.(*ast.AssignStmt)
	return ok && assignmentCalls(assignment, name, validate)
}

func returnCalls(statement *ast.ReturnStmt, name string, validate func(*ast.CallExpr) bool) bool {
	for _, expression := range statement.Results {
		call, ok := expression.(*ast.CallExpr)
		if ok && callName(call.Fun) == name && validate(call) {
			return true
		}
	}
	return false
}

// hasRunnerLoadArguments 要求 repoRoot/options.tree/options.id 精确有序传入。
func hasRunnerLoadArguments(call *ast.CallExpr) bool {
	if len(call.Args) != 3 {
		return false
	}
	if identifier, ok := call.Args[0].(*ast.Ident); !ok || identifier.Name != "repoRoot" {
		return false
	}
	for index, selectorName := range []string{"tree", "id"} {
		selector, ok := call.Args[index+1].(*ast.SelectorExpr)
		if !ok || selector.Sel == nil || selector.Sel.Name != selectorName {
			return false
		}
		owner, ok := selector.X.(*ast.Ident)
		if !ok || owner.Name != "options" {
			return false
		}
	}
	return true
}

func hasIdentifierArgument(call *ast.CallExpr, name string) bool {
	for _, argument := range call.Args {
		identifier, ok := argument.(*ast.Ident)
		if ok && identifier.Name == name {
			return true
		}
	}
	return false
}

func hasExactIdentifierArguments(call *ast.CallExpr, names ...string) bool {
	if len(call.Args) != len(names) {
		return false
	}
	for index, name := range names {
		identifier, ok := call.Args[index].(*ast.Ident)
		if !ok || identifier.Name != name {
			return false
		}
	}
	return true
}

func canonicalImportAlias(file *ast.File, importPath, defaultName string) (string, bool) {
	alias := ""
	for _, importSpec := range file.Imports {
		value, err := strconv.Unquote(importSpec.Path.Value)
		if err != nil || value != importPath || importSpec.Name != nil && (importSpec.Name.Name == "." || importSpec.Name.Name == "_") {
			continue
		}
		if alias != "" {
			return "", false
		}
		alias = defaultName
		if importSpec.Name != nil {
			alias = importSpec.Name.Name
		}
	}
	return alias, alias != ""
}

// functionShadowsName 拒绝函数参数或局部声明遮蔽受信 import alias。
func functionShadowsName(function *ast.FuncDecl, name string) bool {
	if function.Type.Params != nil && fieldsDeclareName(function.Type.Params.List, name) {
		return true
	}
	shadowed := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if shadowed {
			return false
		}
		if nodeDeclaresName(node, name) {
			shadowed = true
			return false
		}
		return true
	})
	return shadowed
}

func fieldsDeclareName(fields []*ast.Field, name string) bool {
	for _, field := range fields {
		for _, identifier := range field.Names {
			if identifier.Name == name {
				return true
			}
		}
	}
	return false
}

func nodeDeclaresName(node ast.Node, name string) bool {
	if assignment, ok := node.(*ast.AssignStmt); ok && assignment.Tok == token.DEFINE {
		for _, left := range assignment.Lhs {
			if isIdentifier(left, name) {
				return true
			}
		}
	}
	statement, ok := node.(*ast.DeclStmt)
	if !ok {
		return false
	}
	declaration, ok := statement.Decl.(*ast.GenDecl)
	if !ok {
		return false
	}
	for _, spec := range declaration.Specs {
		if named, ok := spec.(*ast.ValueSpec); ok && fieldsDeclareName([]*ast.Field{{Names: named.Names}}, name) {
			return true
		}
	}
	return false
}

// hasRunnerWorkloadReturn 要求 load helper 返回 document 与 workload 的平台执行视图。
func hasRunnerWorkloadReturn(function *ast.FuncDecl) bool {
	bound, validated, returned := false, false, false
	for _, statement := range function.Body.List {
		if assignment, ok := statement.(*ast.AssignStmt); ok && len(assignment.Lhs) == 1 && isIdentifier(assignment.Lhs[0], "execution") {
			bound = assignmentCalls(assignment, "runnerPlatformWorkload", func(call *ast.CallExpr) bool {
				return hasExactIdentifierArguments(call, "workload")
			})
		}
		if branch, ok := statement.(*ast.IfStmt); ok && ifInitCalls(branch, "validateRunnerPlatformView", func(call *ast.CallExpr) bool {
			return hasExactIdentifierArguments(call, "workload", "execution")
		}) {
			validated = true
		}
		if isRunnerWorkloadReturn(statement) {
			returned = true
		}
	}
	return bound && validated && returned
}

// isRunnerWorkloadReturn 校验 document 与平台 workload 的精确 return 形状。
func isRunnerWorkloadReturn(statement ast.Stmt) bool {
	result, ok := statement.(*ast.ReturnStmt)
	if !ok || len(result.Results) < 2 || !isIdentifier(result.Results[0], "document") {
		return false
	}
	return isIdentifier(result.Results[1], "execution")
}

// hasTopLevelSelectorAssignment 只接受函数顶层 assignment 中的真实 selector call。
func hasTopLevelSelectorAssignment(function *ast.FuncDecl, packageName, selectorName string, arguments ...string) bool {
	for _, statement := range function.Body.List {
		assignment, ok := statement.(*ast.AssignStmt)
		if !ok {
			continue
		}
		if hasSelectorAssignmentCall(assignment, packageName, selectorName, arguments...) {
			return true
		}
	}
	return false
}

// hasSelectorAssignmentCall 匹配 assignment RHS 上的 package.selector 与标识符参数。
func hasSelectorAssignmentCall(assignment *ast.AssignStmt, packageName, selectorName string, arguments ...string) bool {
	for _, expression := range assignment.Rhs {
		call, ok := expression.(*ast.CallExpr)
		if !ok {
			continue
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel == nil {
			continue
		}
		identifier, ok := selector.X.(*ast.Ident)
		if ok && identifier.Name == packageName && selector.Sel.Name == selectorName && hasNamedArguments(call, arguments...) {
			return true
		}
	}
	return false
}

// hasNamedArguments 判断调用参数是否包含所需标识符集合。
func hasNamedArguments(call *ast.CallExpr, names ...string) bool {
	if len(call.Args) != len(names) {
		return false
	}
	for index, argument := range call.Args {
		identifier, ok := argument.(*ast.Ident)
		if !ok || identifier.Name != names[index] {
			return false
		}
	}
	return true
}

// hasLiveReceiptGuardFlow 要求 helper、候选 tree 调用和 runCommand 转发处于同一活路径。
