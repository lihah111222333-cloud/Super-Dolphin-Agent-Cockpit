//go:build !windows

package codexapp

import (
	"context"
	"strings"
	"syscall"
	"testing"
)

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
	if idxApp < 0 || idxExtra < 0 || idxApp > idxExtra {
		t.Fatalf("argv ordering wrong:\n%s", shellCmd)
	}
}
