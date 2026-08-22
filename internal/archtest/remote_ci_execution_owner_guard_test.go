package archtest

import (
	"go/ast"
	"path/filepath"
	"testing"
)

// TestLocalGateExecutorRemainsSingleEntry locks the retained local Gate
// executor as the only production execution owner after remote dispatch removal.
func TestLocalGateExecutorRemainsSingleEntry(t *testing.T) {
	root := findRepoRoot(t)
	path := filepath.Join(root, "internal", "devtools", "gate", "executor.go")
	file := parseRemoteCIContractGuardFile(t, path)
	if remoteCIFunctionByName(file, "ExecuteExecutor") == nil {
		t.Fatal("local Gate executor entry is missing")
	}
	if count := localGateDeclarationCount(file, "ExecuteExecutor"); count != 1 {
		t.Fatalf("local Gate executor declaration count = %d, want 1", count)
	}
}

func localGateDeclarationCount(file *ast.File, name string) int {
	count := 0
	ast.Inspect(file, func(node ast.Node) bool {
		declaration, ok := node.(*ast.FuncDecl)
		if !ok {
			return true
		}
		if declaration.Recv == nil && declaration.Name != nil && declaration.Name.Name == name {
			count++
		}
		return true
	})
	return count
}
