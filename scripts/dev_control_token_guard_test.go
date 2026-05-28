package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevLaunchersProvideControlSessionToken(t *testing.T) {
	root := filepath.Clean("..")
	assertRunDebugShellProvidesControlSessionToken(t, readRepoFile(t, root, "run-debug.sh"))
	assertRunDebugPowerShellProvidesControlSessionToken(t, readRepoFile(t, root, "run-debug.ps1"))
	assertMakefileProvidesControlSessionToken(t, readRepoFile(t, root, "Makefile"))
}

func assertRunDebugShellProvidesControlSessionToken(t *testing.T, runDebug string) {
	t.Helper()
	for _, want := range []string{
		"ensure_dev_control_session_token()",
		"GO_AGENT_CTL_SESSION_TOKEN=\"$GO_AGENT_MCP_SESSION_TOKEN\"",
		"GO_AGENT_CTL_SESSION_TOKEN=\"dev-local-$(date +%s)-$$\"",
		"ensure_dev_control_session_token",
	} {
		if !strings.Contains(runDebug, want) {
			t.Fatalf("run-debug.sh missing %q", want)
		}
	}

	call := strings.Index(runDebug, "\nensure_dev_control_session_token\n")
	runOnly := strings.Index(runDebug, "# run-only 模式")
	if call < 0 || runOnly < 0 || call > runOnly {
		t.Fatalf("run-debug.sh must initialize GO_AGENT_CTL_SESSION_TOKEN before run-only startup")
	}
}

func assertRunDebugPowerShellProvidesControlSessionToken(t *testing.T, runDebugPS1 string) {
	t.Helper()
	for _, want := range []string{
		"function Ensure-DevControlSessionToken",
		"GO_AGENT_CTL_SESSION_TOKEN",
		"GO_AGENT_MCP_SESSION_TOKEN",
		"dev-local-{0}-{1}-{2}",
		"Ensure-DevControlSessionToken",
		"function Stop-BuildBinaryProcesses",
		"Stop-BuildBinaryProcesses -BuildDir $BuildDir -Names @('mcp-orch', 'mcp-lsp')",
	} {
		if !strings.Contains(runDebugPS1, want) {
			t.Fatalf("run-debug.ps1 missing %q", want)
		}
	}
	psCall := strings.Index(runDebugPS1, "\nEnsure-DevControlSessionToken\n")
	menu := strings.Index(runDebugPS1, "\nWrite-Host '+----------------------------------+'")
	if psCall < 0 || menu < 0 || psCall > menu {
		t.Fatalf("run-debug.ps1 must initialize GO_AGENT_CTL_SESSION_TOKEN before menu startup paths")
	}
	if got := strings.Count(runDebugPS1, "Stop-BuildBinaryProcesses -BuildDir $BuildDir -Names @('mcp-orch', 'mcp-lsp')"); got < 2 {
		t.Fatalf("run-debug.ps1 peer cleanup calls = %d, want run-only and build startup paths", got)
	}
}

func assertMakefileProvidesControlSessionToken(t *testing.T, makefile string) {
	t.Helper()
	for _, want := range []string{
		"DEV_CONTROL_SESSION_TOKEN ?= dev-local-$(shell date +%s)-$(shell echo $$$$)",
		"GO_AGENT_CTL_SESSION_TOKEN=$(DEV_CONTROL_SESSION_TOKEN)",
		"GO_AGENT_PEER_BIN_DIR=$(CURDIR)/bin",
	} {
		if !strings.Contains(makefile, want) {
			t.Fatalf("Makefile missing %q", want)
		}
	}
}

func readRepoFile(t *testing.T, root, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(body)
}
