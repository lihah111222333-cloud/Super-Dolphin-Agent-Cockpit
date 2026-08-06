package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestToolbridgeRuntimeOwnersRemainInstanceScoped blocks the known process-global owner regressions.
func TestToolbridgeRuntimeOwnersRemainInstanceScoped(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	files := map[string]*ast.File{}
	for _, rel := range []string{
		"internal/platform/toolbridge/handler.go",
		"internal/platform/toolbridge/observability_trace.go",
		"internal/platform/toolbridge/http_mcp_client.go",
		"internal/platform/toolbridge/module.go",
		"internal/platform/toolbridge/types.go",
		"internal/platform/toolbridge/handler_peer_decode_helpers.go",
	} {
		files[rel] = toolbridgeOwnerParseFile(t, root, rel)
	}
	violations := toolbridgeForbiddenGlobalViolations(files)
	if !toolbridgeStructHasAtomicField(files["internal/platform/toolbridge/handler.go"], "Handler", "persistentSubagentDefaultFallbackTotal") || !toolbridgeStructHasAtomicField(files["internal/platform/toolbridge/handler.go"], "Handler", "toolTraceSpanSeq") {
		violations = append(violations, "internal/platform/toolbridge/handler.go: Handler must own both atomic runtime counters")
	}
	if !toolbridgeFunctionHasSwitch(files["internal/platform/toolbridge/types.go"], "isCurrentLSPToolName") || !toolbridgeFunctionHasSwitch(files["internal/platform/toolbridge/handler_peer_decode_helpers.go"], "isToolCWDTraceCanonicalTool") || !toolbridgeFunctionHasSwitch(files["internal/platform/toolbridge/handler_peer_decode_helpers.go"], "requiresCanonicalCodexSurfaceTool") {
		violations = append(violations, "toolbridge allow-list predicates must remain pure switches")
	}
	if !toolbridgeFunctionHasOSClientLiteral(files["internal/platform/toolbridge/http_mcp_client.go"], "buildHTTPMCPClient") {
		violations = append(violations, "internal/platform/toolbridge/http_mcp_client.go: buildHTTPMCPClient must construct an instance HTTP client")
	}
	if !toolbridgeFunctionHasProxyAddressParam(files["internal/platform/toolbridge/module.go"], "provideProxyAddrFn") || !toolbridgeFunctionHasProxyAddressParam(files["internal/platform/toolbridge/module.go"], "registerProxyLifecycle") {
		violations = append(violations, "internal/platform/toolbridge/module.go: proxy address must be an explicit owner dependency")
	}
	if len(violations) > 0 {
		t.Fatalf("toolbridge runtime owner violations:\n%s", strings.Join(violations, "\n"))
	}
}

func toolbridgeOwnerParseFile(t *testing.T, root, rel string) *ast.File {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

func toolbridgeForbiddenGlobalViolations(files map[string]*ast.File) []string {
	forbidden := map[string]bool{
		"persistentSubagentDefaultFallbackTotal": true,
		"toolTraceSpanSeq":                       true,
		"defaultHTTPMCPClient":                   true,
		"proxyAddr":                              true,
		"currentLSPToolNames":                    true,
		"toolCWDTraceCanonicalTools":             true,
		"canonicalCodexSurfaceTools":             true,
	}
	var violations []string
	for rel, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range value.Names {
					if forbidden[name.Name] {
						violations = append(violations, fmt.Sprintf("%s: package-level runtime owner %q is forbidden", rel, name.Name))
					}
				}
			}
		}
	}
	return violations
}

func toolbridgeStructHasAtomicField(file *ast.File, structName, fieldName string) bool {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != structName {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				return false
			}
			for _, field := range structType.Fields.List {
				for _, name := range field.Names {
					if name.Name == fieldName && toolbridgeIsAtomicUint64(field.Type) {
						return true
					}
				}
			}
		}
	}
	return false
}

func toolbridgeIsAtomicUint64(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Uint64" {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == "atomic"
}

func toolbridgeFunctionHasSwitch(file *ast.File, name string) bool {
	return toolbridgeFunctionMatches(file, name, func(fn *ast.FuncDecl) bool {
		found := false
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			if _, ok := node.(*ast.SwitchStmt); ok {
				found = true
			}
			return true
		})
		return found
	})
}

func toolbridgeFunctionHasOSClientLiteral(file *ast.File, name string) bool {
	return toolbridgeFunctionMatches(file, name, func(fn *ast.FuncDecl) bool {
		found := false
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			selector, ok := literal.Type.(*ast.SelectorExpr)
			if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "http" && selector.Sel.Name == "Client" {
				found = true
			}
			return true
		})
		return found
	})
}

func toolbridgeFunctionHasProxyAddressParam(file *ast.File, name string) bool {
	return toolbridgeFunctionMatches(file, name, func(fn *ast.FuncDecl) bool {
		for _, field := range fn.Type.Params.List {
			star, ok := field.Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			if ident, ok := star.X.(*ast.Ident); ok && ident.Name == "proxyAddress" {
				return true
			}
		}
		return false
	})
}

func toolbridgeFunctionMatches(file *ast.File, name string, predicate func(*ast.FuncDecl) bool) bool {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return predicate(fn)
		}
	}
	return false
}

func TestToolbridgeRuntimeOwnerGuardDetectsGlobal(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(), "handler.go", `package toolbridge
var toolTraceSpanSeq uint64
`, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse global fixture: %v", err)
	}
	violations := toolbridgeForbiddenGlobalViolations(map[string]*ast.File{"handler.go": file})
	if len(violations) != 1 {
		t.Fatalf("global fixture violations = %#v, want one", violations)
	}
}
