package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestRemoteSubsetExecutorKeepsOneProductionRemoteRunChain 守卫 CLI subset
// 入口：唯一行为差异是 Coordinator.PrepareSubset，其余执行链仍属于既有 owner。
func TestRemoteSubsetExecutorKeepsOneProductionRemoteRunChain(t *testing.T) {
	subset := parseRemoteSubsetRunFunction(t, "executeRemoteRunSubset")
	shared := parseRemoteSubsetRunFunction(t, "executeRemoteRunWithPrepare")
	subsetCalls := remoteSubsetCalls(subset)
	sharedCalls := remoteSubsetCalls(shared)

	subsetShared := requireSingleRemoteRunCall(t, subsetCalls, "executeRemoteRunWithPrepare")
	prepare := requireSingleRemoteRunCall(t, subsetCalls, "PrepareSubset")
	fullShared := requireSingleRemoteRunCall(t, remoteSubsetCalls(parseExecuteRemoteRun(t)), "executeRemoteRunWithPrepare")
	prepared := requireSingleRemoteRunCall(t, sharedCalls, "executePreparedRemoteRun")
	if subsetShared.order == prepare.order || fullShared.order != 0 {
		t.Fatalf("preparer wiring drifted: subset=%d PrepareSubset=%d full=%d", subsetShared.order, prepare.order, fullShared.order)
	}
	if prepared.order == 0 {
		t.Fatal("shared remote run chain must finalize through executePreparedRemoteRun")
	}
	for _, forbidden := range []string{"AgentToken", "resolveRemoteCIAgentToken", "AgentTokenDigest"} {
		if remoteSubsetFunctionMentions(subset, forbidden) {
			t.Fatalf("subset helper must use the already-bound option digest, found %q", forbidden)
		}
	}
}

func remoteSubsetCalls(function *ast.FuncDecl) []remoteRunCall {
	var calls []remoteRunCall
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name := remoteRunCallName(call.Fun); name != "" {
			calls = append(calls, remoteRunCall{name: name, order: len(calls)})
		}
		return true
	})
	return calls
}

func parseRemoteSubsetRunFunction(t *testing.T, name string) *ast.FuncDecl {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), "remote_run_subset.go", nil, 0)
	if err != nil {
		t.Fatalf("parse remote_run_subset.go: %v", err)
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

func remoteSubsetFunctionMentions(function *ast.FuncDecl, identifier string) bool {
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		name, ok := node.(*ast.Ident)
		if ok && name.Name == identifier {
			found = true
		}
		return !found
	})
	return found
}
