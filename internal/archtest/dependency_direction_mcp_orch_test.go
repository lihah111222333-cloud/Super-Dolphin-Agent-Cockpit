package archtest_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

const mcpOrchRPCHostSymbols = ",ApprovalManager,CallbackClient,Dispatch,HTTPRoute,HTTPRouteResult,Module,NewApprovalManager,NewPushBridge,NewServer,NotifyAll,NotifyClient,OnConnect,Params,PushBridge,Register,Run,Server,WSHandler,"
const mcpOrchPkg = "cmd/" + "mcp-orch"

func assertMCPOrchDependencyDirection(t *testing.T, root string) {
	t.Helper()
	files := parseImportFiles(t, root, mcpOrchPkg)
	if len(files) == 0 {
		t.Skip("directory not yet created")
	}
	t.Run("allowed_internal_boundary", func(t *testing.T) {
		allowed := []string{
			internalPrefix("internal/contract"), internalPrefix("internal/dto"), internalPrefix("internal/platform/config"),
			internalPrefix("internal/platform/db"), internalPrefix("internal/platform/bus"), internalPrefix("internal/platform/discovery"), internalPrefix("internal/platform/notify"), internalPrefix("internal/platform/runner"),
			internalPrefix("internal/platform/rpc"), internalPrefix("internal/platform/runtimesafe"), internalPrefix("internal/platform/securefs"), internalPrefix("internal/platform/shared"), internalPrefix("internal/platform/statemachine"), internalPrefix("internal/platform/eventsurface"), internalPrefix("internal/platform/metrics"),
			internalPrefix("internal/platform/rlimit"), internalPrefix("internal/platform/runtimeenv"), internalPrefix("internal/platform/sharedfilefs"), internalPrefix("internal/platform/sharedfilegitignore"), internalPrefix("internal/platform/sharedfilepath"),
			internalPrefix("internal/mcpserver/common"),
			internalPrefix("internal/util"),
		}
		forbidden := []string{
			internalPrefix("internal/app"), modulePath + "/cmd/agent-terminal", modulePath + "/cmd/mcp-lsp", modulePath + "/cmd/mcp-ida",
			internalPrefix("internal/platform/rpc/server"), internalPrefix("internal/platform/rpc/push"), internalPrefix("internal/platform/rpc/notification"),
		}
		var violations []string
		for _, dep := range goListDeps(t, root, mcpOrchPkg) {
			if (!strings.HasPrefix(dep, modulePath+"/internal/") && !strings.HasPrefix(dep, modulePath+"/cmd/")) || strings.HasPrefix(dep, modulePath+"/"+mcpOrchPkg) {
				continue
			}
			if hasAllowedPrefix(dep, forbidden) || !hasAllowedPrefix(dep, allowed) {
				violations = append(violations, fmt.Sprintf("%s depends on %s outside allowed boundary", mcpOrchPkg, dep))
			}
		}
		failIfViolations(t, violations)
	})
	t.Run("no_direct_other_cmd_imports", func(t *testing.T) {
		assertNoImportPrefixes(t, files, []string{modulePath + "/cmd/agent-terminal", modulePath + "/cmd/mcp-lsp", modulePath + "/cmd/mcp-ida"})
	})
	t.Run("rpc_client_mode_only", func(t *testing.T) { assertNoRPCHostSelectors(t, files) })
	t.Run("module_no_reverse_mcp_imports", func(t *testing.T) {
		if !dirExists(root, "internal/module") {
			t.Skip("directory not yet created")
		}
		assertNoImportPrefixes(t, parseImportFiles(t, root, "internal/module"), []string{modulePath + "/" + mcpOrchPkg, modulePath + "/cmd/mcp-lsp", modulePath + "/cmd/mcp-ida"})
	})
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
