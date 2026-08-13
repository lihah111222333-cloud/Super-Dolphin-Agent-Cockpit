//go:build !windows

package codexapp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestBuildPoolSpawnCmdWrapsWithShellUlimit(t *testing.T) {
	t.Parallel()
	cmd, err := BuildPoolSpawnCmd(context.Background(), PoolSpawnArgs{
		Home:      "/realpath/home",
		ParentEnv: standaloneLSPParentEnv(),
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	if len(cmd.Args) < 3 || cmd.Args[0] != "/bin/sh" || cmd.Args[1] != "-c" {
		t.Fatalf("cmd.Args must start with [/bin/sh -c ...], got %v", cmd.Args)
	}
	shellCmd := cmd.Args[2]
	for _, want := range []string{
		"ulimit -n 1048576",
		"exec codex app-server",
		"--listen ws://127.0.0.1:0",
	} {
		if !strings.Contains(shellCmd, want) {
			t.Errorf("shell command missing %q:\n%s", want, shellCmd)
		}
	}
	if !strings.Contains(shellCmd, "ulimit -n 65535") {
		t.Errorf("ulimit fallback missing:\n%s", shellCmd)
	}
}

func TestBuildPoolSpawnCmdAppliesSetpgid(t *testing.T) {
	t.Parallel()
	cmd, err := BuildPoolSpawnCmd(context.Background(), PoolSpawnArgs{
		Home:      "/realpath/home",
		ParentEnv: []string{},
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatalf("Setpgid must be true, got %+v", cmd.SysProcAttr)
	}
	if _, ok := any(cmd.SysProcAttr).(*syscall.SysProcAttr); !ok {
		t.Fatalf("SysProcAttr type unexpected: %T", cmd.SysProcAttr)
	}
}

func TestBuildPoolSpawnCmdAppendsExtraArgs(t *testing.T) {
	t.Parallel()
	cmd, err := BuildPoolSpawnCmd(context.Background(), PoolSpawnArgs{
		Home:      "/realpath/home",
		ExtraArgs: []string{"--log-level", "debug"},
		ParentEnv: []string{},
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	shellCmd := cmd.Args[2]
	if !strings.Contains(shellCmd, "--log-level debug") {
		t.Fatalf("extra args missing:\n%s", shellCmd)
	}
	idxApp := strings.Index(shellCmd, "app-server")
	idxExtra := strings.Index(shellCmd, "--log-level")
	if idxApp < 0 || idxExtra < 0 || idxExtra > idxApp {
		t.Fatalf("argv ordering wrong:\n%s", shellCmd)
	}
}

func TestBuildPoolSpawnCmdShellQuotesWorkspaceRootOverrides(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	workDir := filepath.Join(parent, "folder with spaces")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir work dir: %v", err)
	}
	ctx := withPoolSpawnWorkDir(context.Background(), workDir)
	cmd, err := BuildPoolSpawnCmd(ctx, PoolSpawnArgs{
		Home:      "/realpath/home",
		ParentEnv: standaloneLSPParentEnv(),
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	shellCmd := cmd.Args[2]
	if strings.Contains(shellCmd, "mcp_servers.lsp.cwd="+workDir) {
		t.Fatalf("workspace override with spaces must be shell-quoted:\n%s", shellCmd)
	}
	if !strings.Contains(shellCmd, "'mcp_servers.lsp.cwd=") {
		t.Fatalf("workspace override missing shell-quoted -c value:\n%s", shellCmd)
	}
}

func TestShellQuoteArgEscapesApostrophes(t *testing.T) {
	t.Parallel()
	got := shellQuoteArg("folder's path")
	want := "'folder'\"'\"'s path'"
	if got != want {
		t.Fatalf("shellQuoteArg() = %q, want %q", got, want)
	}
}

func TestBuildPoolSpawnCmdShellQuotesMCPCommandOverrideWithSpacesAndApostrophe(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	workDir := filepath.Join(parent, "project")
	binaryDir := filepath.Join(parent, "bin folder's")
	for _, dir := range []string{workDir, binaryDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", dir, err)
		}
	}
	ctx := withPoolSpawnWorkDir(context.Background(), workDir)
	ctx = withPoolSpawnLSPConfig(ctx, []string{workDir}, binaryDir)
	cmd, err := BuildPoolSpawnCmd(ctx, PoolSpawnArgs{
		Home:      "/realpath/home",
		ParentEnv: standaloneLSPParentEnv(),
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	shellCmd := cmd.Args[2]
	if !strings.Contains(shellCmd, "'mcp_servers.lsp.command=") {
		t.Fatalf("mcp command override missing shell-quoted -c value:\n%s", shellCmd)
	}
	if !strings.Contains(shellCmd, "'\"'\"'") {
		t.Fatalf("mcp command override apostrophe was not escaped safely:\n%s", shellCmd)
	}
}
