package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// remoteCIProductionFiles and the following helpers are shared by the
// remote-CI contract guards; keeping AST traversal separate leaves each guard
// file focused on one contract surface.
func remoteCIProductionFiles(t *testing.T, root string) []string {
	t.Helper()
	return remoteCICollectProductionFiles(t, root, []string{
		"cmd/super-dolphin-gate",
		"internal/devtools/remoteci",
		"internal/devtools/alicloud/eci",
		"internal/devtools/gate",
	}, func(relative string) bool {
		base := filepath.Base(relative)
		return !strings.HasPrefix(relative, "cmd/") || strings.HasPrefix(base, "remote_")
	})
}

func remoteCIContractConsumerFiles(t *testing.T, root string) []string {
	t.Helper()
	return remoteCICollectProductionFiles(t, root, []string{
		"cmd/super-dolphin-gate",
		"internal/devtools/gate",
		"internal/devtools/remoteci",
	}, func(relative string) bool {
		base := filepath.Base(relative)
		return !strings.HasPrefix(relative, "cmd/") || strings.HasPrefix(base, "remote_")
	})
}

func remoteCICollectProductionFiles(t *testing.T, root string, directories []string, include func(string) bool) []string {
	t.Helper()
	var files []string
	for _, directory := range directories {
		absolute := filepath.Join(root, filepath.FromSlash(directory))
		if err := filepath.WalkDir(absolute, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			relative := relativeRemoteCIContractPath(t, root, path)
			if include(relative) {
				files = append(files, path)
			}
			return nil
		}); err != nil {
			t.Fatalf("walk remote CI production directory %s: %v", directory, err)
		}
	}
	return files
}

func parseRemoteCIContractGuardFile(t *testing.T, path string) *ast.File {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return parsed
}

func remoteCIForbiddenIdentifiers(file *ast.File) map[string]bool {
	found := make(map[string]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier != nil {
			found[identifier.Name] = true
		}
		return true
	})
	return found
}

func remoteCIStringLiterals(file *ast.File) []string {
	var values []string
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if ok && literal.Kind == token.STRING {
			values = append(values, strings.Trim(literal.Value, "\"`"))
		}
		return true
	})
	return values
}

func remoteCIImportsContractOwner(file *ast.File) bool {
	for _, importSpec := range file.Imports {
		if strings.Trim(importSpec.Path.Value, "\"") == "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract" {
			return true
		}
	}
	return false
}

func remoteCIRepeatsContractValue(file *ast.File) bool {
	for _, literal := range remoteCIStringLiterals(file) {
		switch literal {
		case "linux/amd64", "claimed", "building", "cache_preparing", "ready_validated", "promoted", "retiring", "failed":
			return true
		}
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		if remoteCIContractDuration(node) {
			found = true
			return false
		}
		return true
	})
	return found
}

func remoteCIContractDuration(node ast.Node) bool {
	expression, ok := node.(*ast.BinaryExpr)
	if !ok || expression.Op != token.MUL {
		return false
	}
	literal, ok := expression.X.(*ast.BasicLit)
	if !ok || literal.Kind != token.INT {
		return false
	}
	selector, ok := expression.Y.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	contractUnit := (literal.Value == "2" && selector.Sel.Name == "Hour") || (literal.Value == "100" && selector.Sel.Name == "Second")
	return remoteCIAll(remoteCIExpressionName(selector.X) == "time", contractUnit)
}

func remoteCIAll(values ...bool) bool {
	for _, value := range values {
		if !value {
			return false
		}
	}
	return true
}

func remoteCIUsesFilesystemStateStore(file *ast.File) bool {
	used := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !remoteCIFileStoreCall(call) {
			return true
		}
		used = true
		return false
	})
	return used
}

func remoteCIFileStoreCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok || packageName.Name != "os" {
		return false
	}
	switch selector.Sel.Name {
	case "ReadFile", "WriteFile", "Open", "OpenFile", "Create":
		return true
	default:
		return false
	}
}

func isECICreateRequest(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "CreateRequest" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && packageName.Name == "eci"
}

func remoteCICreateRequestHasSnapshot(literal *ast.CompositeLit) bool {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := field.Key.(*ast.Ident)
		if ok && key.Name == "ImageCacheSnapshotID" {
			return true
		}
	}
	return false
}

func remoteCIFunctionCalls(file *ast.File, functionName, calledName string) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != functionName {
			continue
		}
		return remoteCIFunctionCallCount(&ast.File{Decls: []ast.Decl{function}}, calledName) != 0
	}
	return false
}
