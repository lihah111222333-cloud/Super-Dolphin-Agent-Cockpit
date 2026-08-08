package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

func TestRuntimeServerArgsIgnoresVolatilePathEntriesWhenGoToolchainIsStable(t *testing.T) {
	goplsBinary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	goBinary := writeRuntimeServerCacheFixture(t, "go", runtimeServerFakeGoEnvScript("stable"))
	toolchainDir := filepath.Dir(goBinary)
	volatileFirst := filepath.Join(t.TempDir(), ".codex", "tmp", "arg0", "first")
	volatileSecond := filepath.Join(t.TempDir(), ".codex", "tmp", "arg0", "second")
	if err := os.MkdirAll(volatileFirst, 0o700); err != nil {
		t.Fatalf("create first volatile PATH entry: %v", err)
	}
	if err := os.MkdirAll(volatileSecond, 0o700); err != nil {
		t.Fatalf("create second volatile PATH entry: %v", err)
	}
	command := multilsp.ServerCommand{
		Executable: "gopls",
		Args:       []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"},
	}
	t.Setenv("PATH", strings.Join([]string{volatileFirst, toolchainDir, toolchainDir}, string(os.PathListSeparator)))
	first := mustRuntimeServerArgs(t, command, goplsBinary, []string{"GOOS=darwin", "GOARCH=arm64"})
	t.Setenv("PATH", strings.Join([]string{volatileSecond, toolchainDir}, string(os.PathListSeparator)))
	second := mustRuntimeServerArgs(t, command, goplsBinary, []string{"GOOS=darwin", "GOARCH=arm64"})
	if runtimeServerGoplsRemoteID(first) != runtimeServerGoplsRemoteID(second) {
		t.Fatalf("volatile PATH entries split one semantic Go toolchain cohort: first=%v second=%v", first, second)
	}
}

func TestRuntimeServerArgsSeparatesDifferentResolvedGoToolchains(t *testing.T) {
	goplsBinary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	firstGo := writeRuntimeServerCacheFixture(t, "go", runtimeServerFakeGoEnvScript("first"))
	secondGo := writeRuntimeServerCacheFixture(t, "go", runtimeServerFakeGoEnvScript("second"))
	command := multilsp.ServerCommand{
		Executable: "gopls",
		Args:       []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"},
	}
	t.Setenv("PATH", filepath.Dir(firstGo))
	first := mustRuntimeServerArgs(t, command, goplsBinary, []string{"GOOS=darwin", "GOARCH=arm64"})
	t.Setenv("PATH", filepath.Dir(secondGo))
	second := mustRuntimeServerArgs(t, command, goplsBinary, []string{"GOOS=darwin", "GOARCH=arm64"})
	if runtimeServerGoplsRemoteID(first) == runtimeServerGoplsRemoteID(second) {
		t.Fatalf("different resolved Go toolchains reused one cohort: first=%v second=%v", first, second)
	}
}
