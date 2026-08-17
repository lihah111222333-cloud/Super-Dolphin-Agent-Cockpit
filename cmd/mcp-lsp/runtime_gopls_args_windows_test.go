//go:build windows

package main

import (
	"reflect"
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

func TestRuntimeServerArgsAddsWindowsKotlinStdioFlag(t *testing.T) {
	command := multilsp.ServerCommand{
		Executable: "kotlin-language-server",
		Args:       []string{"--existing"},
	}
	args, err := runtimeServerArgsPlatform(command, "C:\\cache\\intellij-server.exe", nil)
	if err != nil {
		t.Fatalf("runtimeServerArgsPlatform(kotlin) error = %v", err)
	}
	if len(args) != 2 || args[0] != "--existing" || args[1] != "--stdio" {
		t.Fatalf("runtimeServerArgsPlatform(kotlin) = %#v, want existing args plus --stdio", args)
	}

	command.Args = []string{"--stdio"}
	args, err = runtimeServerArgsPlatform(command, "intellij-server.exe", nil)
	if err != nil {
		t.Fatalf("runtimeServerArgsPlatform(kotlin existing --stdio) error = %v", err)
	}
	if len(args) != 1 || args[0] != "--stdio" {
		t.Fatalf("runtimeServerArgsPlatform(kotlin existing --stdio) = %#v, want one --stdio", args)
	}
}

func TestRuntimeServerArgsUsesEmmyLuaARM64StdioContract(t *testing.T) {
	command := multilsp.ServerCommand{Executable: "lua-language-server"}
	args, err := runtimeServerArgsPlatform(command, "C:\\cache\\emmylua_ls.exe", nil)
	if err != nil {
		t.Fatalf("runtimeServerArgsPlatform(EmmyLua) error = %v", err)
	}
	want := []string{"--communication", "stdio", "--log-level", "error", "--resources-path", "none"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("runtimeServerArgsPlatform(EmmyLua) = %#v, want %#v", args, want)
	}

	command.Executable = "lua-language-server.exe"
	args, err = runtimeServerArgsPlatform(command, "C:\\cache\\emmylua_ls.exe", nil)
	if err != nil {
		t.Fatalf("runtimeServerArgsPlatform(EmmyLua .exe names) error = %v", err)
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("runtimeServerArgsPlatform(EmmyLua .exe names) = %#v, want %#v", args, want)
	}

	args, err = runtimeServerArgsPlatform(command, "C:\\cache\\lua-language-server.exe", nil)
	if err != nil {
		t.Fatalf("runtimeServerArgsPlatform(LuaLS) error = %v", err)
	}
	if len(args) != 0 {
		t.Fatalf("runtimeServerArgsPlatform(LuaLS) = %#v, want unchanged empty args", args)
	}
}
