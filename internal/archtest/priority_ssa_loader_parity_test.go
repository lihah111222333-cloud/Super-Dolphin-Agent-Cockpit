package archtest

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// TestPrioritySSALoaderExtractionPreservesCandidates 固定 seam 的 priority 候选集，并避免在 parity 检查中重复全仓加载。
func TestPrioritySSALoaderExtractionPreservesCandidates(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	loads := 0
	assertPrioritySSALoaderCandidates(t, root, func(path string) ([]string, error) {
		loads++
		return discoverPrioritySSACandidatePaths(path)
	})
	if loads != 1 {
		t.Fatalf("priority loader calls=%d, want 1", loads)
	}
}

func TestPrioritySSALoaderDelegatesToCanonicalDiscoveryOnce(t *testing.T) {
	file := readPrioritySSAScanAST(t)
	loader := findPrioritySSALoader(file)
	if loader == nil {
		t.Fatal("loadPrioritySSAPackages declaration is missing")
	}
	if calls := countPrioritySSADelegations(t, loader.Body); calls != 1 {
		t.Fatalf("production loader discovery delegations=%d, want 1", calls)
	}
}

func readPrioritySSAScanAST(t *testing.T) *ast.File {
	t.Helper()
	source, err := os.ReadFile("priority_ssa_scan.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "priority_ssa_scan.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func findPrioritySSALoader(file *ast.File) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "loadPrioritySSAPackages" {
			return fn
		}
	}
	return nil
}

func countPrioritySSADelegations(t *testing.T, body ast.Node) int {
	t.Helper()
	calls := 0
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		target, ok := call.Fun.(*ast.Ident)
		if !ok || target.Name != "loadPrioritySSAPackagesWithDiscovery" {
			return true
		}
		calls++
		discovery, ok := call.Args[1].(*ast.Ident)
		if !ok || discovery.Name != "discoverPrioritySSACandidatePaths" {
			t.Errorf("production loader must delegate canonical discovery, got %#v", call.Args[1])
		}
		return true
	})
	return calls
}

func assertPrioritySSALoaderCandidates(
	t *testing.T,
	root string,
	load func(string) ([]string, error),
) {
	t.Helper()
	paths, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(paths, "\n"))))
	const wantCount = 260
	const wantDigest = "8693ad04b40932ded14d3a27c9e6a5d6501a00a42ec1844eab84704082ed7f8a"
	if len(paths) != wantCount || digest != wantDigest {
		t.Fatalf("seam cd81d4c9a priority candidates count=%d digest=%s", len(paths), digest)
	}
	for _, required := range []string{
		"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve",
		"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processprobe",
	} {
		if !containsPriorityPath(paths, required) {
			t.Fatalf("priority candidate set is missing %s", required)
		}
	}
}

func containsPriorityPath(paths []string, want string) bool {
	return slices.Contains(paths, want)
}
