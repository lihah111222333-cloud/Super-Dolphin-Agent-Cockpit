//go:build windows

package multilsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestWindowsPythonFormatterUsesWorkspaceScopedProductRoot(t *testing.T) {
	workspace := canonicalScopePath(t.TempDir(), "")
	t.Setenv("SUPER_DOLPHIN_HOME", "")
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		CWD:            workspace,
		WorkspaceRoots: []string{workspace},
	})

	got, err := (&manager{}).resolvePythonFormatterProductRoot(ctx)
	if err != nil {
		t.Fatalf("resolvePythonFormatterProductRoot(): %v", err)
	}
	want := filepath.Join(workspace, ".super-dolphin")
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("resolvePythonFormatterProductRoot() = %q, want workspace-scoped %q", got, want)
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat workspace-scoped formatter product root: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("workspace-scoped formatter product root %q is not a directory", got)
	}
}
