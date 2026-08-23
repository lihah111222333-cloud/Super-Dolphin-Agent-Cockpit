package archtest

import (
	"go/ast"
	"path/filepath"
	"testing"
)

// TestLocalGateWorkerRetainsOnlyCatalogGuard locks the trusted local Gate
// worker surface to the one hook-consumed catalog guard after executor retirement.
func TestLocalGateWorkerRetainsOnlyCatalogGuard(t *testing.T) {
	root := findRepoRoot(t)
	path := filepath.Join(root, "cmd", "super-dolphin-gate", "main.go")
	file := parseRemoteCIContractGuardFile(t, path)
	if remoteCIFunctionByName(file, "runWorkerExecutor") != nil {
		t.Fatal("retired ordinary worker executor remains reachable")
	}
	if remoteCIFunctionByName(file, "runMcpLSPCatalogGuardWorker") == nil {
		t.Fatal("mcp-lsp catalog guard worker entry is missing")
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
