package multilsp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestWaitDiagnosticsStableUsesManagerDeadlineBeforeCallerContext(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsTestFile(t, root, "package.json", `{"name":"diagnostics-manager-deadline"}`)
	target := writeDiagnosticsTestFile(t, root, "app.js", strings.Join([]string{
		"export function value() {",
		"  return 1;",
		"}",
		"",
	}, "\n"))
	mgr := newDiagnosticsTestManager(t, Config{
		WorkspaceRoot:           root,
		DiagnosticsInitialDelay: time.Millisecond,
		DiagnosticsPollInterval: time.Millisecond,
		DiagnosticsMaxWait:      20 * time.Millisecond,
	})
	ctx, cancel := diagnosticsDeadlineContext(root, 250*time.Millisecond)
	defer cancel()
	uri := fileURIFromPath(target)

	started := time.Now()
	err := mgr.WaitDiagnosticsStable(ctx, []string{uri})
	elapsed := time.Since(started)
	if err == nil || !errors.Is(err, lspmanager.ErrDiagnosticsNotReady) {
		t.Fatalf("WaitDiagnosticsStable() error = %v, want ErrDiagnosticsNotReady", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("caller context expired before manager deadline returned: %v", ctx.Err())
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("WaitDiagnosticsStable() elapsed = %s, want manager deadline before caller context", elapsed)
	}
}

func diagnosticsDeadlineContext(root string, timeout time.Duration) (context.Context, context.CancelFunc) {
	scope := common.ToolScope{CWD: root, WorkspaceRoots: []string{root}}
	return context.WithTimeout(common.WithToolScope(context.Background(), scope), timeout)
}
