package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestExecuteRemoteRunKeepsPreparedAllHitAndMissPathsOnOneFinalizedRun 守卫两阶段生产接线：
// executeRemoteRun 只选择 full Prepare，而共享 preparation owner 先完成复用决策再进入
// prepared executor；all-hit 在 MISS 绑定 helper 内提前返回，MISS 才绑定 execution identity、
// 校准并重载计划，最终都执行 RunPrepared 与证据最终化。
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
	run := requireSingleRemoteRunCall(t, preparedCalls, "runPrepared")
	finalize := requireSingleRemoteRunCall(t, preparedCalls, "finalizeEvidence")
	ensure := requireSingleRemoteRunCall(t, reloadCalls, "ensureRemoteDurationCalibration")
	reload := requireSingleRemoteRunCall(t, reloadCalls, "ReloadPlanningSnapshot")

	if executePrepared.order <= prepare.order {
		t.Fatalf("prepared executor order = %d, Prepare order = %d; executor must follow reuse decision", executePrepared.order, prepare.order)
	}
	assertRemotePreparedCallOrder(t, bind, calibration, run, finalize)
	if reload.order <= ensure.order {
		t.Fatalf("planning reload order = %d, calibration order = %d; planning reload must follow calibration", reload.order, ensure.order)
	}
	if ensure.condition != "" || reload.condition != "" {
		t.Fatalf("miss-only helper must call calibration and planning reload only after its early return: ensure=%q reload=%q", ensure.condition, reload.condition)
	}
	requireRemoteRunEarlyReturnGuard(t, bindFunction.Body, "dependencies.allReused()")
	requireRemoteRunEarlyReturnGuard(t, reloadFunction.Body, "prepared.AllReused() || input.Calibration")
}

// assertRemotePreparedCallOrder 验证统一 prepared executor 的无条件终态链。
func assertRemotePreparedCallOrder(t *testing.T, bind, calibration, run, finalize remoteRunCall) {
	t.Helper()
	if !(bind.order < calibration.order && calibration.order < run.order && run.order < finalize.order) {
		t.Fatalf("prepared call order = bind:%d calibration:%d run:%d finalize:%d", bind.order, calibration.order, run.order, finalize.order)
	}
	if bind.condition != "" || calibration.condition != "" || run.condition != "" || finalize.condition != "" {
		t.Fatalf("prepared calls must be unconditional: bind=%q calibration=%q run=%q finalize=%q", bind.condition, calibration.condition, run.condition, finalize.condition)
	}
}

type remoteRunCall struct {
	name      string
	order     int
	condition string
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
	*calls = append(*calls, remoteRunCall{name: name, order: len(*calls), condition: condition})
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
