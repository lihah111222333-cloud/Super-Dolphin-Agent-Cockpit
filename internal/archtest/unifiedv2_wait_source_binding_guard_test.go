package archtest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

const unifiedV2WaitCandidatePath = "cmd/mcp-orch/tools/unifiedv2/wait.go"

// TestUnifiedV2WaitCandidateBindsSelectorsToTargetSource keeps the disabled
// unified_v2 candidate aligned with the cross-task read contract: caller
// identity is checked against grant.Source, while event selectors and durable
// handles are checked against grant.Target's canonical source tuple.
func TestUnifiedV2WaitCandidateBindsSelectorsToTargetSource(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	path := filepath.Join(root, filepath.FromSlash(unifiedV2WaitCandidatePath))
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse unified_v2 wait candidate %s: %v", path, err)
	}
	functions := unifiedV2WaitCandidateFunctions(file)
	for _, name := range []string{"grantMatches", "matchesTarget", "validateTarget"} {
		if functions[name] == nil {
			t.Fatalf("unified_v2 wait candidate is missing %s", name)
		}
	}
	if !unifiedV2WaitFunctionCalls(functions["grantMatches"], "targetSourceMatchesFence") {
		t.Fatal("grantMatches must bind the selector source to grant.Target's fence")
	}
	if !unifiedV2WaitFunctionCalls(functions["matchesTarget"], "targetSourceMatchesFence") {
		t.Fatal("matchesTarget must recheck the selector source against the target fence")
	}
	if !unifiedV2WaitFunctionCalls(functions["validateTarget"], "targetSourceTupleMatchesSelector") {
		t.Fatal("validateTarget must enforce the canonical target source tuple")
	}
	for _, name := range []string{"grantMatches", "matchesTarget", "validateTarget"} {
		if unifiedV2WaitComparesSelectorToCallerSource(functions[name].Body) {
			t.Fatalf("%s compares selector source to caller source", name)
		}
	}
}

func unifiedV2WaitCandidateFunctions(file *ast.File) map[string]*ast.FuncDecl {
	functions := make(map[string]*ast.FuncDecl)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Body != nil {
			functions[function.Name.Name] = function
		}
	}
	return functions
}

func unifiedV2WaitFunctionCalls(function *ast.FuncDecl, want string) bool {
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if identifier, ok := call.Fun.(*ast.Ident); ok && identifier.Name == want {
			found = true
		}
		return true
	})
	return found
}

func unifiedV2WaitComparesSelectorToCallerSource(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	compares := false
	ast.Inspect(body, func(node ast.Node) bool {
		binary, ok := node.(*ast.BinaryExpr)
		if !ok || !isEquality(binary.Op) {
			return true
		}
		if selectorCallerSourceComparison(binary.X, binary.Y) {
			compares = true
		}
		return true
	})
	return compares
}

// isEquality 判断 AST 二元表达式是否为相等或不等比较。
func isEquality(op token.Token) bool { return op == token.EQL || op == token.NEQ }

// selectorCallerSourceComparison 判断 selector.SourceID/epoch 是否直接和 caller source 比较。
func selectorCallerSourceComparison(leftExpr, rightExpr ast.Expr) bool {
	left, right := unifiedV2WaitSelectorPath(leftExpr), unifiedV2WaitSelectorPath(rightExpr)
	if left == "" || right == "" {
		return false
	}
	return sourcePathPair(left, right, ".Selector.SourceID", "source.SourceID") ||
		sourcePathPair(left, right, ".Selector.SourceEpoch", "source.SourceEpoch")
}

// sourcePathPair 处理左右操作数交换后的 selector/caller source 比较。
func sourcePathPair(left, right, selectorSuffix, callerPath string) bool {
	return strings.HasSuffix(left, selectorSuffix) && right == callerPath ||
		strings.HasSuffix(right, selectorSuffix) && left == callerPath
}

func unifiedV2WaitSelectorPath(expression ast.Expr) string {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if nested, ok := selector.X.(*ast.SelectorExpr); ok {
		if root, ok := nested.X.(*ast.Ident); ok {
			return root.Name + "." + nested.Sel.Name + "." + selector.Sel.Name
		}
	}
	if root, ok := selector.X.(*ast.Ident); ok {
		return root.Name + "." + selector.Sel.Name
	}
	return ""
}
