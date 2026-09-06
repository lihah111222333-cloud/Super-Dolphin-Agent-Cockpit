//go:build darwin || linux

package standalone

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// TestCLIWorkspaceResolvesAliases 在执行器真实入口检查链接逃逸，不能只验证帮助函数。
func TestCLIWorkspaceResolvesAliases(t *testing.T) {
	cfg := validTestConfig(t)
	inside := filepath.Join(cfg.WorkspaceRoot, "inside")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(cfg.WorkspaceRoot, "escape")
	if err := os.Symlink(t.TempDir(), escape); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "workspace-alias")
	if err := os.Symlink(cfg.WorkspaceRoot, alias); err != nil {
		t.Fatal(err)
	}
	e := NewExecution(cfg)
	for _, tc := range []struct {
		cwd      string
		accepted bool
	}{
		{cfg.WorkspaceRoot, true}, {inside, true}, {alias, true},
		{escape, false}, {filepath.Join(cfg.WorkspaceRoot, "missing"), false},
	} {
		req := contract.LaunchRequest{Cwd: tc.cwd, Env: []string{"AGENT_PROVIDER=codex", "AGENT_MODEL=test-model"}}
		_, _, err := e.launchProfile(req)
		if (err == nil) != tc.accepted {
			t.Fatalf("cwd=%q accepted=%v error=%v", tc.cwd, tc.accepted, err)
		}
	}
}
