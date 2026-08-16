//go:build windows

package main

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

func TestRuntimeServerGoplsRootCohortConfigSeparatesDaemonIdleTimeoutsOnWindows(t *testing.T) {
	root := t.TempDir()
	binary := writeRuntimeServerCacheFixture(t, "gopls.exe", "test gopls binary")
	firstCommand := multilsp.ServerCommand{
		Executable: "gopls.exe",
		Args:       []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"},
	}
	secondCommand := firstCommand
	secondCommand.Args = []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=2s"}

	first, err := runtimeServerGoplsRootCohortConfig(firstCommand, binary, root, []string{"GOOS=windows", "GOARCH=arm64"})
	if err != nil {
		t.Fatalf("runtimeServerGoplsRootCohortConfig(first) error = %v", err)
	}
	second, err := runtimeServerGoplsRootCohortConfig(secondCommand, binary, root, []string{"GOOS=windows", "GOARCH=arm64"})
	if err != nil {
		t.Fatalf("runtimeServerGoplsRootCohortConfig(second) error = %v", err)
	}
	if first.EffectiveConfigDigest == second.EffectiveConfigDigest {
		t.Fatalf("different daemon idle timeouts reused effective config digest: first=%#v second=%#v", first, second)
	}
	if first.CohortID == second.CohortID {
		t.Fatalf("different daemon idle timeouts reused cohort ID: first=%#v second=%#v", first, second)
	}
}

func TestRuntimeServerArgsFailsFastWithoutWorkspaceRootOnWindows(t *testing.T) {
	command := multilsp.ServerCommand{
		Executable: "gopls.exe",
		Args:       []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"},
	}
	_, err := runtimeServerArgs(command, "gopls.exe", nil)
	if err == nil || !strings.Contains(err.Error(), "requires one workspace root") {
		t.Fatalf("runtimeServerArgs() error = %v, want workspace-root requirement", err)
	}
}
