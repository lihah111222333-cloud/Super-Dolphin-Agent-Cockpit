package codexapp

import (
	"context"
	"strings"
	"syscall"
	"testing"
)

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

func TestBuildPoolSpawnCmdWrapsWithShellUlimit(t *testing.T) {
	t.Parallel()
	cmd, err := BuildPoolSpawnCmd(context.Background(), PoolSpawnArgs{
		Home:      "/realpath/home",
		ParentEnv: []string{},
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	if len(cmd.Args) < 3 || cmd.Args[0] != "sh" || cmd.Args[1] != "-c" {
		t.Fatalf("cmd.Args must start with [sh -c ...], got %v", cmd.Args)
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
	// ulimit fallback must be present so low-privilege shells still
	// make progress.
	if !strings.Contains(shellCmd, "ulimit -n 65535") {
		t.Errorf("ulimit fallback missing:\n%s", shellCmd)
	}
}

func TestBuildPoolSpawnCmdInjectsCODEXHOME(t *testing.T) {
	t.Parallel()
	cmd, err := BuildPoolSpawnCmd(context.Background(), PoolSpawnArgs{
		Home: "/canonical/home",
		ParentEnv: []string{
			"PATH=/usr/bin",
			"HOME=/home/user",
			"CODEX_HOME=/stale/leak", // must be shadowed by the override
			"OPENAI_API_KEY=secret",    // must be dropped
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
	if strings.Contains(env, "OPENAI_API_KEY=") {
		t.Fatalf("non-allowlisted env leaked:\n%s", env)
	}
	for _, must := range []string{"PATH=/usr/bin", "HOME=/home/user"} {
		if !strings.Contains(env, must) {
			t.Errorf("allowlisted env %q dropped:\n%s", must, env)
		}
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
	// Double-check type assertion so a future Setpgid addition to a
	// fake SysProcAttr doesn't silently drift.
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
	// Verify base argv ordering is preserved: app-server must come
	// before --log-level, not after.
	idxApp := strings.Index(shellCmd, "app-server")
	idxExtra := strings.Index(shellCmd, "--log-level")
	if idxApp < 0 || idxExtra < 0 || idxApp > idxExtra {
		t.Fatalf("argv ordering wrong:\n%s", shellCmd)
	}
}

func TestBuildPoolSpawnCmdDefaultsParentEnvToOSEnviron(t *testing.T) {
	// Not Parallel: t.Setenv mutates process env and the stdlib
	// testing framework forbids combining it with t.Parallel.
	t.Setenv("OPENAI_API_KEY", "should-be-dropped")
	t.Setenv("TZ", "UTC") // on the allowlist
	cmd, err := BuildPoolSpawnCmd(context.Background(), PoolSpawnArgs{
		Home: "/realpath/home",
		// ParentEnv intentionally nil -> defaults to os.Environ()
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	env := strings.Join(cmd.Env, "\n")
	if strings.Contains(env, "OPENAI_API_KEY=") {
		t.Fatalf("OS-Environ default should still filter rogue keys:\n%s", env)
	}
	if !strings.Contains(env, "TZ=UTC") {
		t.Fatalf("allowlisted OS-Environ value missing:\n%s", env)
	}
}
