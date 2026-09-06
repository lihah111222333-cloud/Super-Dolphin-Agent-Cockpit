package archtest_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

func TestTimeoutLocality(t *testing.T) {
	root := repoRoot(t)
	violations := collectTimeoutLocalityViolations(t, root)
	failIfViolations(t, violations)
}

func collectTimeoutLocalityViolations(t *testing.T, root string) []string {
	t.Helper()
	var violations []string
	for _, absPath := range walkGoFiles(t, root, "internal", "cmd", "scripts") {
		relPath := timeoutLocalityRelPath(t, root, absPath)
		if allowedTimeoutFile(relPath) {
			continue
		}
		violations = append(violations, timeoutLocalityFileViolations(t, absPath, relPath)...)
	}
	return violations
}

func timeoutLocalityRelPath(t *testing.T, root, absPath string) string {
	t.Helper()
	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		t.Fatalf("rel path for %s: %v", absPath, err)
	}
	return filepath.ToSlash(relPath)
}

func timeoutLocalityFileViolations(t *testing.T, absPath, relPath string) []string {
	t.Helper()
	fset := token.NewFileSet()
	fileNode, err := parser.ParseFile(fset, absPath, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", relPath, err)
	}
	var violations []string
	ast.Inspect(fileNode, func(n ast.Node) bool {
		if timeoutCall, ok := contextWithTimeoutCall(n); ok {
			violations = append(violations, fmt.Sprintf("%s:%d uses context.WithTimeout outside a registered timeout owner", relPath, fset.Position(timeoutCall.Pos()).Line))
		}
		return true
	})
	return violations
}

func contextWithTimeoutCall(n ast.Node) (*ast.CallExpr, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil || sel.Sel.Name != "WithTimeout" {
		return nil, false
	}
	ident, ok := sel.X.(*ast.Ident)
	return call, ok && ident.Name == "context"
}

func allowedTimeoutFile(relPath string) bool {
	if reason, ok := registeredTimeoutOwners[relPath]; ok {
		return strings.TrimSpace(reason) != ""
	}
	return strings.HasPrefix(relPath, "internal/transport/retry/")
}

// registeredTimeoutOwners 记录每个允许直接创建 timeout context 的局部 owner 及其预算来源。
// 每个条目都必须是精确文件路径；新增 timeout 不得通过目录级白名单绕过 owner 审查。
var registeredTimeoutOwners = map[string]string{
	"cmd/mcp-orch/serve.go":                                         "standalone orch lifecycle owns startup and shutdown deadlines",
	"cmd/mcp-recovery-custody/service.go":                           "independent RecoveryCustody lifecycle owns bounded HTTP shutdown",
	"internal/mcpserver/common/http_local_runner.go":                "local MCP runner owns bounded HTTP shutdown",
	"internal/platform/config/timeouts.go":                          "platform shared timeout wrappers own ctxutil budgets",
	"internal/devtools/gateprivate/timeouts.go":                     "trusted Gate private timeout wrapper owns gate budgets",
	"internal/platform/toolbridge/schema/client.go":                 "schema client owns operation and reap deadlines",
	"internal/util/ctxutil/ctxutil.go":                              "shared backend timeout constants and wrappers own defaults",
	"cmd/mcp-lsp/installer/windows_asset_cache_shared.go":           "WindowsAssetCache owns the caller-provided HTTP asset timeout",
	"cmd/mcp-lsp/installer/windows_runtime_dependency_provision.go": "runtime dependency provision owns install timeout and cache lock deadline",
	"cmd/mcp-lsp/multilsp/client.go":                                "multilsp client owns semantic workspace readiness timeout",
	"cmd/mcp-lsp/runtime_windows_markdown_client.go":                "Windows Markdown client owns parser and watcher request deadlines",
	"internal/devtools/heterocinode/batch.go":                       "Wine batch cleanup owns the bounded wineserver cleanup timeout",
	"internal/devtools/heterocinode/linux_facts.go":                 "Linux facts provider owns the Docker command timeout",
	"internal/devtools/heterocitransport/ssh_requester.go":          "SSH probe requester owns the per-probe transport timeout",
	"internal/devtools/heterociworkflow/run.go":                     "workflow owns receipt-read and cleanup deadlines",
	"internal/devtools/remotecicli/portable_validate.go":            "portable validation composition owns its bounded catalog and bind deadlines",
	"internal/devtools/remotecicli/postgresql_authority.go":         "execute composition owns the caller-provided request timeout",
	"internal/devtools/remoteruntime/transport.go":                  "runtime transport owns request and operation deadlines",
}
