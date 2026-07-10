package archtest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

func TestContractDoesNotDeclareLegacyAgentLifecyclePort(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(
		fset,
		filepath.Join(repoRoot(t), "internal/contract/orchestration.go"),
		nil,
		0,
	)
	if err != nil {
		t.Fatalf("parse orchestration contract: %v", err)
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if ok && typeSpec.Name.Name == "AgentLifecyclePort" {
				t.Fatal("legacy AgentLifecyclePort must be replaced by workflow ports")
			}
		}
	}
}
