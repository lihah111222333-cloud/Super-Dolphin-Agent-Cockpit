package codexapp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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

func standaloneLSPParentEnv(extra ...string) []string {
	return append([]string{
		"SUPER_DOLPHIN_RUNTIME_MODE=dev",
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=/work/repo",
	}, extra...)
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

func TestBuildPoolSpawnCmdUsesAbsoluteSystemShellForFDLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix fd-limit wrapper is not used on Windows")
	}
	userDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(userDir, "sh"), []byte("#!/bin/sh\nexit 99\n"), 0o755); err != nil {
		t.Fatalf("write malicious sh: %v", err)
	}

	cmd, err := BuildPoolSpawnCmd(context.Background(), PoolSpawnArgs{
		Home: "/realpath/home",
		ParentEnv: []string{
			"PATH=" + userDir,
		},
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	if cmd.Path != "/bin/sh" {
		t.Fatalf("cmd.Path = %q, want /bin/sh", cmd.Path)
	}
	if len(cmd.Args) == 0 || cmd.Args[0] != "/bin/sh" {
		t.Fatalf("cmd.Args = %#v, want /bin/sh argv0", cmd.Args)
	}
}

func TestEnsureCodexCLIAvailableReportsAutoInstallFailureWhenReleaseHasNoAsset(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	server := newCodexReleaseTestServer(t, codexReleaseTestOptions{})
	t.Setenv(codexTrustedReleaseMirrorEnvForTest, "1")
	t.Setenv(codexReleaseAPIURLEnv, server.URL+"/latest")
	t.Setenv(codexReleaseSHA256EnvForTest, strings.Repeat("a", 64))
	t.Setenv(codexInstallRootEnv, t.TempDir())

	err := ensureCodexCLIAvailable(context.Background())
	if err == nil {
		t.Fatal("ensureCodexCLIAvailable() error = nil, want auto-install failure")
	}
	msg := err.Error()
	for _, want := range []string{
		"codex CLI not found in PATH",
		"automatic install from official OpenAI GitHub",
		"https://github.com/openai/codex",
		"no compatible OpenAI Codex release asset",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("ensureCodexCLIAvailable() error missing %q:\n%s", want, msg)
		}
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

func TestBuildPoolSpawnCmdPropagatesSidecarRuntimeContract(t *testing.T) {
	cmd, err := BuildPoolSpawnCmd(context.Background(), PoolSpawnArgs{
		Home: "/canonical/home",
		ParentEnv: []string{
			"PATH=/usr/bin",
			"SUPER_DOLPHIN_RUNTIME_MODE=dev",
			"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=/work/repo",
			"SUPER_DOLPHIN_DEPENDENCY_PROFILE=desktop_host",
		},
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	env := strings.Join(cmd.Env, "\n")
	for _, want := range []string{
		"SUPER_DOLPHIN_RUNTIME_MODE=dev",
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=/work/repo",
		"SUPER_DOLPHIN_DEPENDENCY_PROFILE=desktop_host",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("sidecar runtime contract env %q missing:\n%s", want, env)
		}
	}
}

func TestBuildPoolSpawnCmdDefaultsParentEnvToOSEnviron(t *testing.T) {
	// Not Parallel: t.Setenv mutates process env and the stdlib
	// testing framework forbids combining it with t.Parallel.
	t.Setenv("OPENAI_API_KEY", "from-os-env")
	setCodexDatabaseEnvForTest(t)
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
	requireCodexDatabaseEnvAbsent(t, cmd.Env)
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
			"SUPER_DOLPHIN_RUNTIME_MODE=dev",
			"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=/work/repo",
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
	parent := t.TempDir()
	workDir := filepath.Join(parent, "project with space")
	appHome := filepath.Join(parent, "Library", "Application Support", "Super Dolphin")
	t.Setenv("SUPER_DOLPHIN_HOME", appHome)
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "dev")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", parent)
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
		ParentEnv: standaloneLSPParentEnv("SUPER_DOLPHIN_HOME=" + appHome),
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
		"mcp_servers.lsp.env.SUPER_DOLPHIN_HOME=",
		"mcp_servers.lsp.env.SUPER_DOLPHIN_RUNTIME_MODE=\"dev\"",
		"mcp_servers.lsp.env.SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=",
		"mcp_servers.lsp.env.SUPER_DOLPHIN_DEPENDENCY_PROFILE=\"production\"",
		realWorkDir,
		appHome,
	} {
		if !commandLineContains(commandLine, want) {
			t.Fatalf("spawn argv missing %q:\n%s", want, commandLine)
		}
	}
	env := strings.Join(cmd.Env, "\n")
	if !strings.Contains(env, "SUPER_DOLPHIN_HOME="+appHome) {
		t.Fatalf("spawn env missing SUPER_DOLPHIN_HOME:\n%s", env)
	}
}

func TestBuildPoolSpawnCmdOverridesModelProvider(t *testing.T) {
	t.Parallel()
	cmd, err := BuildPoolSpawnCmd(context.Background(), PoolSpawnArgs{
		Home:      "/realpath/home",
		ExtraArgs: poolSpawnNativeLSPConfigOverrideArgs([]string{"model_provider=" + tomlString("openai")}),
		ParentEnv: standaloneLSPParentEnv(),
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	commandLine := strings.Join(cmd.Args, " ")
	for _, want := range []string{
		"-c",
		`model_provider="openai"`,
	} {
		if !commandLineContains(commandLine, want) {
			t.Fatalf("spawn argv missing %q:\n%s", want, commandLine)
		}
	}
}

func TestBuildPoolSpawnCmdPlacesConfigOverridesBeforeAppServer(t *testing.T) {
	t.Parallel()
	cmd, err := BuildPoolSpawnCmd(context.Background(), PoolSpawnArgs{
		Home:      "/realpath/home",
		ExtraArgs: poolSpawnNativeLSPConfigOverrideArgs([]string{"model_provider=" + tomlString("openai")}),
		ParentEnv: standaloneLSPParentEnv(),
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	commandLine := strings.Join(cmd.Args, " ")
	providerAt := strings.Index(commandLine, "model_provider=")
	appServerAt := strings.Index(commandLine, codexAppServerCommand)
	if providerAt < 0 || appServerAt < 0 || providerAt > appServerAt {
		t.Fatalf("config override must appear before app-server; providerAt=%d appServerAt=%d:\n%s", providerAt, appServerAt, commandLine)
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
		ParentEnv: standaloneLSPParentEnv(),
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

func TestBuildPoolSpawnCmdRejectsNativeLSPConfigWithoutRuntimeContract(t *testing.T) {
	t.Parallel()
	binaryDir := filepath.Join(t.TempDir(), "mcp-bin")
	ctx := withPoolSpawnLSPConfig(context.Background(), nil, binaryDir)
	_, err := BuildPoolSpawnCmd(ctx, PoolSpawnArgs{
		Home:      "/realpath/home",
		ParentEnv: []string{"PATH=/usr/bin"},
	})
	if err == nil || !strings.Contains(err.Error(), "SUPER_DOLPHIN_RUNTIME_MODE") {
		t.Fatalf("BuildPoolSpawnCmd missing runtime contract error = %v", err)
	}
}

func TestBuildPoolSpawnCmdOverridesNativeLSPConfigWithEmptyRoots(t *testing.T) {
	t.Parallel()
	binaryDir := filepath.Join(t.TempDir(), "mcp bin")
	ctx := withPoolSpawnLSPConfig(context.Background(), nil, binaryDir)
	cmd, err := BuildPoolSpawnCmd(ctx, PoolSpawnArgs{
		Home:      "/realpath/home",
		ParentEnv: standaloneLSPParentEnv(),
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
