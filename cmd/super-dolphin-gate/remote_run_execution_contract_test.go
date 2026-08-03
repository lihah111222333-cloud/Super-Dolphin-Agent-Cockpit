package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestExecuteRemoteRunKeepsPreparedAllHitAndMissPathsOnOneFinalizedRun 守卫正常运行接线：
// 全命中跳过自动校准和计划刷新后仍须到达 RunPrepared 与最终化；未命中只能在唯一 Prepare
// 决策后经 miss-only helper 校准、刷新计划，再回到同一个 finalized run。
func TestExecuteRemoteRunKeepsPreparedAllHitAndMissPathsOnOneFinalizedRun(t *testing.T) {
	executeCalls := remoteRunCalls(parseExecuteRemoteRun(t).Body)
	refreshFunction := parseRemoteRunFunction(t, "refreshRemotePlanningAfterCalibration")
	refreshCalls := remoteRunCalls(refreshFunction.Body)

	prepare := requireSingleRemoteRunCall(t, executeCalls, "Prepare")
	calibration := requireSingleRemoteRunCall(t, executeCalls, "refreshRemotePlanningAfterCalibration")
	run := requireSingleRemoteRunCall(t, executeCalls, "RunPrepared")
	finalize := requireSingleRemoteRunCall(t, executeCalls, "finalizeRemoteRunEvidence")
	ensure := requireSingleRemoteRunCall(t, refreshCalls, "ensureRemoteDurationCalibration")
	refresh := requireSingleRemoteRunCall(t, refreshCalls, "RefreshPlanningSnapshot")

	if calibration.order <= prepare.order {
		t.Fatalf("miss-only calibration helper order = %d, Prepare order = %d; calibration must follow Prepare", calibration.order, prepare.order)
	}
	if refresh.order <= ensure.order {
		t.Fatalf("planning refresh order = %d, calibration order = %d; planning refresh must follow calibration", refresh.order, ensure.order)
	}
	if run.order <= calibration.order {
		t.Fatalf("RunPrepared order = %d, calibration helper order = %d; both all-hit and miss paths must converge on RunPrepared", run.order, calibration.order)
	}
	if finalize.order <= run.order {
		t.Fatalf("finalize order = %d, RunPrepared order = %d; every prepared run must finalize evidence", finalize.order, run.order)
	}
	if run.condition != "" || finalize.condition != "" {
		t.Fatalf("RunPrepared/finalize must be unconditional after the reuse branch: run=%q finalize=%q", run.condition, finalize.condition)
	}

	if calibration.condition != "" || run.condition != "" || finalize.condition != "" {
		t.Fatalf("calibration helper, RunPrepared, and finalizer must be unconditional after Prepare: helper=%q run=%q finalize=%q", calibration.condition, run.condition, finalize.condition)
	}
	if ensure.condition != "" || refresh.condition != "" {
		t.Fatalf("miss-only helper must call calibration and planning refresh only after its early return: ensure=%q refresh=%q", ensure.condition, refresh.condition)
	}
	requireRemoteRunEarlyReturnGuard(t, refreshFunction.Body, "prepared.AllReused() || input.Calibration || input.SelectedTests")
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
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), "remote_run.go", nil, 0)
	if err != nil {
		t.Fatalf("parse remote_run.go: %v", err)
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
			if returned, ok := guard.Body.List[0].(*ast.ReturnStmt); ok && len(returned.Results) == 1 {
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
				for _, expression := range value.Rhs {
					appendRemoteRunCall(&calls, expression, condition)
				}
			case *ast.DeferStmt:
				appendRemoteRunCall(&calls, value.Call, condition)
			case *ast.IfStmt:
				if initialization, ok := value.Init.(*ast.AssignStmt); ok {
					for _, expression := range initialization.Rhs {
						appendRemoteRunCall(&calls, expression, condition)
					}
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
