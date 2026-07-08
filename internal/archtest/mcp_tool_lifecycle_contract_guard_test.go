package archtest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

func TestToolbridgeDoesNotOwnMCPToolLifecycleStore(t *testing.T) {
	t.Parallel()

	files := parseImportFiles(t, repoRoot(t), "internal/platform/toolbridge")
	assertNoImportPrefixes(t, files, []string{internalPrefix("internal/store/mcpserver")})
}

func TestMCPToolLifecyclePolicyPortStaysInContractLayer(t *testing.T) {
	t.Parallel()

	path := filepath.Join(repoRoot(t), "internal", "contract", "mcp_control.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse MCP control contract: %v", err)
	}

	required := map[string]bool{
		"MCPToolLifecyclePolicyReader":  false,
		"MCPToolLifecyclePolicyRequest": false,
	}
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if _, ok := required[typeSpec.Name.Name]; ok {
				required[typeSpec.Name.Name] = true
			}
		}
	}
	for name, found := range required {
		if !found {
			t.Fatalf("%s must stay in internal/contract/mcp_control.go so toolbridge depends on the contract port, not store ownership", name)
		}
	}
}
