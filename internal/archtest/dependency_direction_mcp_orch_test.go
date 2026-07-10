package archtest_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

const mcpOrchRPCHostSymbols = ",ApprovalManager,CallbackClient,Dispatch,HTTPRoute,HTTPRouteResult,Module,NewApprovalManager,NewPushBridge,NewServer,NotifyAll,NotifyClient,OnConnect,Params,PushBridge,Register,Run,Server,WSHandler,"
const mcpOrchPkg = "cmd/" + "mcp-orch"

func assertMCPOrchDependencyDirection(t *testing.T, root string) {
	t.Helper()
	files := parseImportFiles(t, root, mcpOrchPkg)
	if len(files) == 0 {
		t.Skip("directory not yet created")
	}
	registry := archtest.DefaultBackendBoundaryRegistry()
	t.Run("direct_internal_boundary", func(t *testing.T) {
		assertMCPOrchDirectImportBoundary(t, files, registry)
	})
	t.Run("transitive_internal_boundary", func(t *testing.T) {
		assertMCPOrchTransitiveImportBoundary(t, root, registry)
	})
	t.Run("rpc_client_mode_only", func(t *testing.T) { assertNoRPCHostSelectors(t, files) })
	t.Run("module_no_reverse_mcp_imports", func(t *testing.T) {
		if !dirExists(root, "internal/module") {
			t.Skip("directory not yet created")
		}
		assertNoImportPrefixes(t, parseImportFiles(t, root, "internal/module"), []string{modulePath + "/" + mcpOrchPkg, modulePath + "/cmd/mcp-lsp", modulePath + "/cmd/mcp-ida"})
	})
}

func assertMCPOrchDirectImportBoundary(t *testing.T, files []parsedFile, registry archtest.BackendBoundaryRegistry) {
	t.Helper()
	var violations []string
	for _, file := range files {
		fileViolations, err := archtest.EvaluateBackendBoundaryFile(
			file.AbsPath,
			file.RelPath,
			registry,
			"mcp_sidecar_narrow_import_surface",
		)
		if err != nil {
			t.Fatalf("EvaluateBackendBoundaryFile(%s): %v", file.RelPath, err)
		}
		violations = append(violations, fileViolations...)
	}
	failIfViolations(t, violations)
}

func assertMCPOrchTransitiveImportBoundary(t *testing.T, root string, registry archtest.BackendBoundaryRegistry) {
	t.Helper()
	imports := mcpOrchBoundaryImports(goListDeps(t, root, mcpOrchPkg))
	if len(imports) == 0 {
		return
	}
	path := filepath.Join(t.TempDir(), "mcp_orch_transitive.go")
	if err := os.WriteFile(path, []byte(mcpOrchImportFixtureSource(imports)), 0o600); err != nil {
		t.Fatalf("write transitive fixture: %v", err)
	}
	violations, err := archtest.EvaluateBackendBoundaryFile(
		path,
		mcpOrchPkg+"/main.go",
		registry,
		"mcp_sidecar_narrow_import_surface",
	)
	if err != nil {
		t.Fatalf("EvaluateBackendBoundaryFile(transitive): %v", err)
	}
	failIfViolations(t, violations)
}

func mcpOrchBoundaryImports(dependencies []string) []string {
	var imports []string
	for _, dependency := range dependencies {
		if !isMCPOrchBoundaryImport(dependency) {
			continue
		}
		imports = append(imports, dependency)
	}
	return imports
}

func isMCPOrchBoundaryImport(dependency string) bool {
	if strings.HasPrefix(dependency, modulePath+"/"+mcpOrchPkg) {
		return false
	}
	return strings.HasPrefix(dependency, modulePath+"/internal/") || strings.HasPrefix(dependency, modulePath+"/cmd/")
}

func mcpOrchImportFixtureSource(imports []string) string {
	var source strings.Builder
	source.WriteString("package fixture\nimport (\n")
	for _, importPath := range imports {
		source.WriteString("\t_ ")
		source.WriteString(strconv.Quote(importPath))
		source.WriteString("\n")
	}
	source.WriteString(")\n")
	return source.String()
}

func assertNoRPCHostSelectors(t *testing.T, files []parsedFile) {
	t.Helper()
	var violations []string
	for _, file := range files {
		node, err := parser.ParseFile(token.NewFileSet(), file.AbsPath, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", file.RelPath, err)
		}
		aliases := rpcImportAliases(t, node)
		violations = append(violations, rpcAliasViolations(file, aliases)...)
		violations = append(violations, rpcHostSelectorViolations(file, node, aliases)...)
	}
	failIfViolations(t, violations)
}

func rpcImportAliases(t *testing.T, node *ast.File) map[string]bool {
	t.Helper()
	aliases := map[string]bool{}
	for _, spec := range node.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("unquote import %s: %v", spec.Path.Value, err)
		}
		if path != internalPrefix("internal/platform/rpc") {
			continue
		}
		if spec.Name != nil {
			aliases[spec.Name.Name] = true
			continue
		}
		aliases["rpc"] = true
	}
	return aliases
}

func rpcAliasViolations(file parsedFile, aliases map[string]bool) []string {
	if aliases["."] {
		return []string{fmt.Sprintf("%s dot-imports internal/platform/rpc", file.RelPath)}
	}
	return nil
}

func rpcHostSelectorViolations(file parsedFile, node *ast.File, aliases map[string]bool) []string {
	var violations []string
	ast.Inspect(node, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, okID := sel.X.(*ast.Ident)
		if okID && aliases[id.Name] && strings.Contains(mcpOrchRPCHostSymbols, ","+sel.Sel.Name+",") {
			violations = append(violations, fmt.Sprintf("%s uses internal/platform/rpc host symbol %s", file.RelPath, sel.Sel.Name))
		}
		return true
	})
	return violations
}
