package httpclient

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

func TestHTTPClientPackageHasNoForbiddenRuntimeDependencies(t *testing.T) {
	packages, err := parser.ParseDir(token.NewFileSet(), ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("ParseDir() error = %v", err)
	}
	for _, parsedPackage := range packages {
		for fileName, file := range parsedPackage.Files {
			ast.Inspect(file, func(node ast.Node) bool {
				importSpec, ok := node.(*ast.ImportSpec)
				if !ok {
					return true
				}
				path, unquoteErr := strconv.Unquote(importSpec.Path.Value)
				if unquoteErr != nil {
					t.Errorf("%s invalid import: %v", fileName, unquoteErr)
					return false
				}
				for _, forbidden := range []string{
					"sqliteauthority", "/tools", "/store", "database/sql", "go.uber.org/fx",
					"internal/mcpserver", "modelcontextprotocol", "/mcpwire",
				} {
					if strings.Contains(path, forbidden) {
						t.Errorf("%s imports forbidden dependency %q", fileName, path)
					}
				}
				return false
			})
		}
	}
}
