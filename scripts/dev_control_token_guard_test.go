package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevLaunchersProvideControlSessionToken(t *testing.T) {
	root := filepath.Clean("..")
	runDebug := readRepoFile(t, root, "run-debug.sh")
	makefile := readRepoFile(t, root, "Makefile")

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
