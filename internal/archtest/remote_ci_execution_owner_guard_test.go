package archtest

import (
	"go/ast"
	"path/filepath"
	"testing"
)

// TestRemoteCIGateUsesSharedExecutionOwner 锁定 Gate 只能通过共享 owner 串联
// Coordinator 执行与 SQLite authority finalizer，禁止调用方重新拆成两条路径。
func TestRemoteCIGateUsesSharedExecutionOwner(t *testing.T) {
	root := findRepoRoot(t)
	parsed := parseRemoteCIContractGuardFile(t, filepath.Join(root, "cmd", "super-dolphin-gate", "remote_run.go"))
	defaults := remoteCIFunctionByName(parsed, "defaultRemotePreparedRunDependencies")
	if defaults == nil {
		t.Fatal("defaultRemotePreparedRunDependencies is missing")
	}
	if got := remoteCIFunctionCallCount(&ast.File{Decls: []ast.Decl{defaults}}, "NewExecutionOwner"); got != 1 {
		t.Fatalf("shared execution owner constructor calls = %d, want 1", got)
	}
	execute := remoteCIFunctionByName(parsed, "executePreparedRemoteRun")
	if execute == nil {
		t.Fatal("executePreparedRemoteRun is missing")
	}
	file := &ast.File{Decls: []ast.Decl{execute}}
	if got := remoteCIFunctionCallCount(file, "ExecutePrepared"); got != 1 {
		t.Fatalf("shared execution owner calls = %d, want 1", got)
	}
	for _, forbidden := range []string{"runPrepared", "finalizeEvidence", "RunPrepared", "finalizeRemoteRunEvidence"} {
		if got := remoteCIFunctionCallCount(file, forbidden); got != 0 {
			t.Fatalf("executePreparedRemoteRun directly calls %s %d times", forbidden, got)
		}
	}
}
