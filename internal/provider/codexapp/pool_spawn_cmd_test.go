package codexapp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func commandLineContains(commandLine, want string) bool {
	if strings.Contains(commandLine, want) {
		return true
	}
	escaped := strings.ReplaceAll(want, `\`, `\\`)
	if escaped != want && strings.Contains(commandLine, escaped) {
		return true
	}
	doubleEscaped := strings.ReplaceAll(want, `\`, `\\\\`)
	return doubleEscaped != want && strings.Contains(commandLine, doubleEscaped)
}

func TestBuildPoolSpawnCmdRequiresHome(t *testing.T) {
	t.Parallel()
	_, err := BuildPoolSpawnCmd(context.Background(), PoolSpawnArgs{})
	if err == nil {
		t.Fatal("empty home should error")
	}
	_, err = BuildPoolSpawnCmd(context.Background(), PoolSpawnArgs{Home: "   "})
	if err == nil {
		t.Fatal("whitespace-only home should error")
	}
}

func TestBuildPoolSpawnCmdInjectsCODEXHOME(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "secret")
	cmd, err := BuildPoolSpawnCmd(context.Background(), PoolSpawnArgs{
		Home: "/canonical/home",
		ParentEnv: []string{
			"PATH=/usr/bin",
			"HOME=/home/user",
			"CODEX_HOME=/stale/leak", // must be shadowed by the override
		},
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	env := strings.Join(cmd.Env, "\n")
	if !strings.Contains(env, "CODEX_HOME=/canonical/home") {
		t.Fatalf("canonical CODEX_HOME missing:\n%s", env)
	}
	if strings.Contains(env, "CODEX_HOME=/stale/leak") {
		t.Fatalf("stale CODEX_HOME should be shadowed:\n%s", env)
	}
	if !strings.Contains(env, "OPENAI_API_KEY=secret") {
		t.Fatalf("OPENAI_API_KEY missing:\n%s", env)
	}
	for _, must := range []string{"PATH=/usr/bin", "HOME=/home/user"} {
		if !strings.Contains(env, must) {
			t.Errorf("allowlisted env %q dropped:\n%s", must, env)
		}
	}
}

func TestBuildPoolSpawnCmdDefaultsParentEnvToOSEnviron(t *testing.T) {
	// Not Parallel: t.Setenv mutates process env and the stdlib
	// testing framework forbids combining it with t.Parallel.
	t.Setenv("OPENAI_API_KEY", "from-os-env")
	t.Setenv("TZ", "UTC") // on the allowlist
	cmd, err := BuildPoolSpawnCmd(context.Background(), PoolSpawnArgs{
		Home: "/realpath/home",
		// ParentEnv intentionally nil -> defaults to os.Environ()
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	env := strings.Join(cmd.Env, "\n")
	if !strings.Contains(env, "OPENAI_API_KEY=from-os-env") {
		t.Fatalf("OS-Environ OPENAI_API_KEY missing:\n%s", env)
	}
	if !strings.Contains(env, "TZ=UTC") {
		t.Fatalf("allowlisted OS-Environ value missing:\n%s", env)
	}
}

func TestBuildPoolSpawnCmdSetsWorkDirAndPWD(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()
	realWorkDir, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatalf("realpath work dir: %v", err)
	}
	ctx := withPoolSpawnWorkDir(context.Background(), workDir)
	cmd, err := BuildPoolSpawnCmd(ctx, PoolSpawnArgs{
		Home: "/realpath/home",
		ParentEnv: []string{
			"PATH=/usr/bin",
			"PWD=/stale/workdir",
		},
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	if cmd.Dir != realWorkDir {
		t.Fatalf("cmd.Dir = %q, want %q", cmd.Dir, realWorkDir)
	}
	env := strings.Join(cmd.Env, "\n")
	if !strings.Contains(env, "PWD="+realWorkDir) {
		t.Fatalf("PWD override missing:\n%s", env)
	}
	if strings.Contains(env, "PWD=/stale/workdir") {
		t.Fatalf("stale PWD leaked:\n%s", env)
	}
}

func TestBuildPoolSpawnCmdOverridesNativeLSPConfigForWorkDir(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	workDir := filepath.Join(parent, "project with space")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir work dir: %v", err)
	}
	realWorkDir, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatalf("realpath work dir: %v", err)
	}
	ctx := withPoolSpawnWorkDir(context.Background(), workDir)
	cmd, err := BuildPoolSpawnCmd(ctx, PoolSpawnArgs{
		Home:      "/realpath/home",
		ParentEnv: []string{},
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	commandLine := strings.Join(cmd.Args, " ")
	for _, want := range []string{
		"-c",
		"mcp_servers.lsp.cwd=",
		"mcp_servers.lsp.env.GO_AGENT_LSP_ROOT=",
		"mcp_servers.lsp.env.GO_AGENT_LSP_ROOTS=",
		realWorkDir,
	} {
		if !commandLineContains(commandLine, want) {
			t.Fatalf("spawn argv missing %q:\n%s", want, commandLine)
		}
	}
}

func TestBuildPoolSpawnCmdOverridesNativeLSPConfigForAdditionalRootsAndBinaryDir(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	workDir := filepath.Join(parent, "primary project")
	extraDir := filepath.Join(parent, "extra project")
	binaryDir := filepath.Join(parent, "mcp bin")
	for _, dir := range []string{workDir, extraDir, binaryDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", dir, err)
		}
	}
	ctx := withPoolSpawnWorkDir(context.Background(), workDir)
	ctx = withPoolSpawnLSPConfig(ctx, []string{workDir, extraDir}, binaryDir)
	cmd, err := BuildPoolSpawnCmd(ctx, PoolSpawnArgs{
		Home:      "/realpath/home",
		ParentEnv: []string{},
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	commandLine := strings.Join(cmd.Args, " ")
	for _, want := range []string{
		"mcp_servers.lsp.command=",
		filepath.Join(binaryDir, "mcp-lsp"),
		"mcp_servers.lsp.env.GO_AGENT_LSP_ROOTS=",
		extraDir,
	} {
		if !commandLineContains(commandLine, want) {
			t.Fatalf("spawn argv missing %q:\n%s", want, commandLine)
		}
	}
}

func TestBuildPoolSpawnCmdOverridesNativeLSPConfigWithEmptyRoots(t *testing.T) {
	t.Parallel()
	binaryDir := filepath.Join(t.TempDir(), "mcp bin")
	ctx := withPoolSpawnLSPConfig(context.Background(), nil, binaryDir)
	cmd, err := BuildPoolSpawnCmd(ctx, PoolSpawnArgs{
		Home:      "/realpath/home",
		ParentEnv: []string{},
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	commandLine := strings.Join(cmd.Args, " ")
	for _, want := range []string{
		"mcp_servers.lsp.command=",
		filepath.Join(binaryDir, "mcp-lsp"),
		"mcp_servers.lsp.type=",
		"mcp_servers.lsp.env.GO_AGENT_LSP_ROOTS=",
		"[]",
	} {
		if !commandLineContains(commandLine, want) {
			t.Fatalf("spawn argv missing %q:\n%s", want, commandLine)
		}
	}
	for _, forbidden := range []string{
		"mcp_servers.lsp.cwd=",
		"mcp_servers.lsp.env.GO_AGENT_LSP_ROOT=",
	} {
		if strings.Contains(commandLine, forbidden) {
			t.Fatalf("spawn argv contains untrusted root override %q:\n%s", forbidden, commandLine)
		}
	}
}

func TestLocalSpawnAppServerArgsFailCloseNativeLSPConfig(t *testing.T) {
	t.Parallel()
	args := localSpawnAppServerArgs()
	commandLine := strings.Join(args, " ")
	for _, want := range []string{
		"app-server",
		"-c",
		"mcp_servers.lsp.command=",
		"mcp-lsp",
		"mcp_servers.lsp.type=",
		"mcp_servers.lsp.env.GO_AGENT_LSP_ROOTS=",
		"[]",
	} {
		if !commandLineContains(commandLine, want) {
			t.Fatalf("local spawn args missing %q:\n%s", want, commandLine)
		}
	}
	for _, forbidden := range []string{
		"mcp_servers.lsp.cwd=",
		"mcp_servers.lsp.env.GO_AGENT_LSP_ROOT=",
	} {
		if strings.Contains(commandLine, forbidden) {
			t.Fatalf("local spawn args contain untrusted root override %q:\n%s", forbidden, commandLine)
		}
	}
}

func TestBuildPoolSpawnCmdRejectsRelativeWorkDir(t *testing.T) {
	t.Parallel()
	ctx := withPoolSpawnWorkDir(context.Background(), "relative/workdir")
	if _, err := BuildPoolSpawnCmd(ctx, PoolSpawnArgs{Home: "/realpath/home"}); err == nil {
		t.Fatal("relative work dir should error")
	}
}
