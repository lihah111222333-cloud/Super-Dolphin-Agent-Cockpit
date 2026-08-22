package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestExecuteRemoteRunKeepsPreparedAllHitAndMissPathsOnOneFinalizedRun 守卫两阶段生产接线：
// executeRemoteRun 先完成复用决策，再在 MISS 边界明确阻断旧 ImageCache successor；
// all-hit 仍沿既有 prepared executor 终态链运行。
func TestExecuteRemoteRunKeepsPreparedAllHitAndMissPathsOnOneFinalizedRun(t *testing.T) {
	executeCalls := remoteRunCalls(parseExecuteRemoteRun(t).Body)
	prepareFunction := parseRemoteRunSubsetOwner(t, "executeRemoteRunWithPrepare")
	prepareCalls := remoteRunCalls(prepareFunction.Body)
	preparedFunction := parseRemoteRunFunction(t, "executePreparedRemoteRun")
	preparedCalls := remoteRunCalls(preparedFunction.Body)
	bindFunction := parseRemoteRunFunction(t, "bindRemoteMissExecutionInputs")
	reloadFunction := parseRemoteRunFunction(t, "reloadRemotePlanningAfterCalibration")
	reloadCalls := remoteRunCalls(reloadFunction.Body)

	requireSingleRemoteRunCall(t, executeCalls, "executeRemoteRunWithPrepare")
	prepare := requireSingleRemoteRunCall(t, prepareCalls, "prepare")
	executePrepared := requireSingleRemoteRunCall(t, prepareCalls, "executePreparedRemoteRun")
	bind := requireSingleRemoteRunCall(t, preparedCalls, "bindRemoteMissExecutionInputs")
	calibration := requireSingleRemoteRunCall(t, preparedCalls, "reloadPlanning")
	run := requireSingleRemoteRunCall(t, preparedCalls, "ExecutePrepared")
	ensure := requireSingleRemoteRunCall(t, reloadCalls, "ensureRemoteDurationCalibration")
	refresh := requireSingleRemoteRunCall(t, reloadCalls, "RefreshWorkloadPassesAfterCalibration")
	reload := requireSingleRemoteRunCall(t, reloadCalls, "ReloadPlanningSnapshot")

	assertRemotePreparedExecutorOrder(t, prepare, executePrepared)
	assertRemotePreparedLifecycle(t, prepareFunction, prepare, executePrepared)
	assertRetiredRemotePreparedOrder(t, calibration, bind, run)
	assertRemoteCalibrationOrder(t, ensure, refresh, reload)
	requireRemoteRunEarlyReturnGuard(t, bindFunction.Body, "dependencies.allReused()")
	requireRemoteRunEarlyReturnGuard(t, reloadFunction.Body, "prepared.AllReused() || input.Calibration || input.SelectedTests")
}

func assertRemotePreparedExecutorOrder(t *testing.T, prepare, execute remoteRunCall) {
	t.Helper()
	if execute.order <= prepare.order {
		t.Fatalf("prepared executor order = %d, Prepare order = %d; executor must follow reuse decision", execute.order, prepare.order)
	}
}

func assertRemotePreparedLifecycle(t *testing.T, prepareFunction *ast.FuncDecl, prepare, execute remoteRunCall) {
	t.Helper()
	clearPosition := requireRemoteRunInventorySnapshotClear(t, prepareFunction.Body)
	if !(prepare.position < clearPosition && clearPosition < execute.position) {
		t.Fatalf("inventory snapshot clear position = %d, want between Prepare %d and execute %d", clearPosition, prepare.position, execute.position)
	}
	requireRemoteRunReturnedInputCall(t, prepareFunction.Body, "executePreparedRemoteRun", 4, "input")
}

func assertRetiredRemotePreparedOrder(t *testing.T, calibration, bind, execute remoteRunCall) {
	t.Helper()
	if !(calibration.order < bind.order && bind.order < execute.order) {
		t.Fatalf("prepared call order = calibration:%d bind:%d execute:%d", calibration.order, bind.order, execute.order)
	}
	if calibration.condition != "" || bind.condition != "" || execute.condition != "" {
		t.Fatalf("prepared calls must be unconditional: calibration=%q bind=%q execute=%q", calibration.condition, bind.condition, execute.condition)
	}
}

func assertRemoteCalibrationOrder(t *testing.T, ensure, refresh, reload remoteRunCall) {
	t.Helper()
	if !(ensure.order < refresh.order && refresh.order < reload.order) {
		t.Fatalf("post-calibration order = ensure:%d refresh:%d reload:%d", ensure.order, refresh.order, reload.order)
	}
	if ensure.condition != "" || refresh.condition != "" || reload.condition != "" {
		t.Fatalf("miss-only helper calls must follow its early return: ensure=%q refresh=%q reload=%q", ensure.condition, refresh.condition, reload.condition)
	}
}

// TestAutomaticCalibrationDropsInventorySnapshotsBeforeRetention 守卫自动校准只保留
// 三次运行所需的 inventory/identity，不把 transient exact-tree snapshot 存入数组。
func TestAutomaticCalibrationDropsInventorySnapshotsBeforeRetention(t *testing.T) {
	function := parseRemoteRunFunctionFromFile(t, "remote_run_calibration_automation.go", "tryAcceptAutomaticRemoteCalibration")
	requireRemoteRunInputArrayLength(t, function.Body, "inputs", "3")
	resolvePosition := requireRemoteRunCallPosition(t, function.Body, "resolveRemoteRunInput")
	clearPosition := requireRemoteRunInventorySnapshotClear(t, function.Body)
	storePosition := requireRemoteRunInputStore(t, function.Body, "inputs", "input")
	if !(resolvePosition < clearPosition && clearPosition < storePosition) {
		t.Fatalf("automatic calibration input order = resolve:%d clear:%d store:%d", resolvePosition, clearPosition, storePosition)
	}
}

type remoteRunCall struct {
	name      string
	order     int
	condition string
	position  token.Pos
}

func parseExecuteRemoteRun(t *testing.T) *ast.FuncDecl {
	return parseRemoteRunFunction(t, "executeRemoteRun")
}

func parseRemoteRunFunction(t *testing.T, name string) *ast.FuncDecl {
	return parseRemoteRunFunctionFromFile(t, "remote_run.go", name)
}

func parseRemoteRunSubsetOwner(t *testing.T, name string) *ast.FuncDecl {
	return parseRemoteRunFunctionFromFile(t, "remote_run_subset.go", name)
}

func parseRemoteRunFunctionFromFile(t *testing.T, fileName, name string) *ast.FuncDecl {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), fileName, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", fileName, err)
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			return function
		}
	}
	t.Fatalf("%s declaration not found", name)
	return nil
}

func requireRemoteRunEarlyReturnGuard(t *testing.T, body *ast.BlockStmt, wantCondition string) {
	t.Helper()
	for _, statement := range body.List {
		guard, ok := statement.(*ast.IfStmt)
		if !ok || remoteRunCondition(guard.Cond) != wantCondition {
			continue
		}
		if len(guard.Body.List) == 1 {
			if _, ok := guard.Body.List[0].(*ast.ReturnStmt); ok {
				return
			}
		}
		t.Fatalf("miss-only guard %q must return immediately", wantCondition)
	}
	t.Fatalf("miss-only early return guard %q not found", wantCondition)
}

func remoteRunCalls(body *ast.BlockStmt) []remoteRunCall {
	var calls []remoteRunCall
	var walkStatements func([]ast.Stmt, string)
	walkStatements = func(statements []ast.Stmt, condition string) {
		for _, statement := range statements {
			switch value := statement.(type) {
			case *ast.ExprStmt:
				appendRemoteRunCall(&calls, value.X, condition)
			case *ast.AssignStmt:
				appendRemoteRunExpressions(&calls, value.Rhs, condition)
			case *ast.ReturnStmt:
				appendRemoteRunExpressions(&calls, value.Results, condition)
			case *ast.DeferStmt:
				appendRemoteRunCall(&calls, value.Call, condition)
			case *ast.IfStmt:
				if initialization, ok := value.Init.(*ast.AssignStmt); ok {
					appendRemoteRunExpressions(&calls, initialization.Rhs, condition)
				}
				branchCondition := remoteRunCondition(value.Cond)
				walkStatements(value.Body.List, branchCondition)
				if alternate, ok := value.Else.(*ast.BlockStmt); ok {
					walkStatements(alternate.List, branchCondition)
				}
			}
		}
	}
	walkStatements(body.List, "")
	return calls
}

// appendRemoteRunExpressions 依序登记一个表达式列表中的直接调用。
func appendRemoteRunExpressions(calls *[]remoteRunCall, expressions []ast.Expr, condition string) {
	for _, expression := range expressions {
		appendRemoteRunCall(calls, expression, condition)
	}
}

func appendRemoteRunCall(calls *[]remoteRunCall, expression ast.Expr, condition string) {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return
	}
	name := remoteRunCallName(call.Fun)
	if name == "" {
		return
	}
	*calls = append(*calls, remoteRunCall{name: name, order: len(*calls), condition: condition, position: call.Pos()})
}

func requireRemoteRunInventorySnapshotClear(t *testing.T, body *ast.BlockStmt) token.Pos {
	t.Helper()
	var positions []token.Pos
	ast.Inspect(body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if ok && remoteRunClearsInventorySnapshot(assignment) {
			positions = append(positions, assignment.Pos())
		}
		return true
	})
	if len(positions) != 1 {
		t.Fatalf("input.InventorySnapshot clear assignments = %d, want 1", len(positions))
	}
	return positions[0]
}

func remoteRunClearsInventorySnapshot(assignment *ast.AssignStmt) bool {
	if assignment == nil || assignment.Tok != token.ASSIGN || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return false
	}
	selector, ok := assignment.Lhs[0].(*ast.SelectorExpr)
	if !ok {
		return false
	}
	input, inputOK := selector.X.(*ast.Ident)
	zero, zeroOK := assignment.Rhs[0].(*ast.Ident)
	return inputOK && zeroOK && input.Name == "input" && selector.Sel.Name == "InventorySnapshot" && zero.Name == "nil"
}

func requireRemoteRunCallPosition(t *testing.T, body *ast.BlockStmt, name string) token.Pos {
	t.Helper()
	var positions []token.Pos
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && remoteRunCallName(call.Fun) == name {
			positions = append(positions, call.Pos())
		}
		return true
	})
	if len(positions) != 1 {
		t.Fatalf("%s call positions = %d, want 1", name, len(positions))
	}
	return positions[0]
}

func requireRemoteRunInputStore(t *testing.T, body *ast.BlockStmt, target, source string) token.Pos {
	t.Helper()
	var positions []token.Pos
	ast.Inspect(body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if ok && remoteRunStoresInput(assignment, target, source) {
			positions = append(positions, assignment.Pos())
		}
		return true
	})
	if len(positions) != 1 {
		t.Fatalf("%s indexed input stores = %d, want 1", target, len(positions))
	}
	return positions[0]
}

func remoteRunStoresInput(assignment *ast.AssignStmt, target, source string) bool {
	if assignment == nil || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return false
	}
	indexed, ok := assignment.Lhs[0].(*ast.IndexExpr)
	if !ok {
		return false
	}
	targetName, targetOK := indexed.X.(*ast.Ident)
	sourceName, sourceOK := assignment.Rhs[0].(*ast.Ident)
	return targetOK && sourceOK && targetName.Name == target && sourceName.Name == source
}

func requireRemoteRunInputArrayLength(t *testing.T, body *ast.BlockStmt, name, want string) {
	t.Helper()
	var found int
	ast.Inspect(body, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != name {
			return true
		}
		array, ok := spec.Type.(*ast.ArrayType)
		if !ok || array.Len == nil {
			return true
		}
		length, lengthOK := array.Len.(*ast.BasicLit)
		if lengthOK && length.Value == want {
			found++
		}
		return true
	})
	if found != 1 {
		t.Fatalf("%s fixed array length %s declarations = %d, want 1", name, want, found)
	}
}

func requireRemoteRunReturnedInputCall(t *testing.T, body *ast.BlockStmt, name string, argument int, want string) {
	t.Helper()
	var found int
	for _, statement := range body.List {
		returned, ok := statement.(*ast.ReturnStmt)
		if !ok || len(returned.Results) != 1 {
			continue
		}
		call, ok := returned.Results[0].(*ast.CallExpr)
		if !ok || remoteRunCallName(call.Fun) != name {
			continue
		}
		if argument >= len(call.Args) {
			t.Fatalf("%s argument index %d exceeds %d arguments", name, argument, len(call.Args))
		}
		input, ok := call.Args[argument].(*ast.Ident)
		if !ok || input.Name != want {
			t.Fatalf("%s argument %d is %T, want identifier %q", name, argument, call.Args[argument], want)
		}
		found++
	}
	if found != 1 {
		t.Fatalf("directly returned %s calls using %q = %d, want 1", name, want, found)
	}
}

func remoteRunCallName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	default:
		return ""
	}
}

func remoteRunCondition(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.BinaryExpr:
		return remoteRunCondition(value.X) + " " + value.Op.String() + " " + remoteRunCondition(value.Y)
	case *ast.UnaryExpr:
		return value.Op.String() + remoteRunCondition(value.X)
	case *ast.CallExpr:
		return remoteRunCallExpression(value.Fun) + "()"
	case *ast.SelectorExpr:
		return remoteRunCondition(value.X) + "." + value.Sel.Name
	case *ast.Ident:
		return value.Name
	default:
		return ""
	}
}

func remoteRunCallExpression(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return remoteRunCondition(value.X) + "." + value.Sel.Name
	default:
		return ""
	}
}

func requireSingleRemoteRunCall(t *testing.T, calls []remoteRunCall, name string) remoteRunCall {
	t.Helper()
	var found []remoteRunCall
	for _, call := range calls {
		if call.name == name {
			found = append(found, call)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s calls = %d, want 1: %#v", name, len(found), found)
	}
	return found[0]
}
