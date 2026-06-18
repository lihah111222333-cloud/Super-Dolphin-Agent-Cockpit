package main

import (
	"strings"
	"testing"
)

func TestNewUIDesktopLaunchersProvideControlSessionToken(t *testing.T) {
	assertNewUIDesktopShellProvidesControlSessionToken(t, readScript(t, "../run-new-ui-desktop.sh"))
	assertNewUIDesktopPowerShellProvidesControlSessionToken(t, readScript(t, "../run-new-ui-desktop.ps1"))
	assertMakefileProvidesControlSessionToken(t, readScript(t, "../Makefile"))
}

func assertNewUIDesktopShellProvidesControlSessionToken(t *testing.T, script string) {
	t.Helper()
	for _, want := range []string{
		"ensure_dev_control_session_token()",
		`export GO_AGENT_CTL_SESSION_TOKEN="$GO_AGENT_MCP_SESSION_TOKEN"`,
		`export GO_AGENT_CTL_SESSION_TOKEN="dev-new-ui-$(date +%s)-$$"`,
		"ensure_dev_control_session_token",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("run-new-ui-desktop.sh missing %q", want)
		}
	}
	assertScriptOrder(t, script, "\nensure_dev_control_session_token\n", "\nensure_sqlite_runtime\n")
}

func assertNewUIDesktopPowerShellProvidesControlSessionToken(t *testing.T, script string) {
	t.Helper()
	script = strings.ReplaceAll(script, "\r\n", "\n")
	for _, want := range []string{
		"function Ensure-DevControlSessionToken",
		"GO_AGENT_CTL_SESSION_TOKEN",
		"GO_AGENT_MCP_SESSION_TOKEN",
		"dev-new-ui-{0}-{1}",
		"Ensure-DevControlSessionToken",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("run-new-ui-desktop.ps1 missing %q", want)
		}
	}
	assertScriptOrderAfter(t, script, "\n    Add-CodexCliToPath\n", "\n    Ensure-DevControlSessionToken\n", "\n    Ensure-SqliteRuntime\n")
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
