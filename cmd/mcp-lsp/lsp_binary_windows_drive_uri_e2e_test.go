//go:build windows && e2e

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLSPBinaryWindowsDriveFileURIReachesStrictServer verifies that the real
// MCP binary sends RFC 8089 drive-letter URIs through the LSP initialize and
// document-symbol boundary.
// TestLSPBinaryWindowsGoSemanticActionTiming correlates each Go navigation
// request with the native fake-gopls invocation/PID evidence.
func TestLSPBinaryWindowsGoSemanticActionTiming(t *testing.T) {
	skipLSPBinaryResidualE2EInShortMode(t)
	root := canonicalToolTestRoot(t, t.TempDir())
	writeLSPBinaryFixture(t, filepath.Join(root, "go.mod"), "module example.com/windows-timing\n\ngo 1.26\n")
	target := filepath.Join(root, "main.go")
	writeLSPBinaryFixture(t, target, "package main\n\nfunc TimingValue() string { return \"ready\" }\n")
	install := buildWindowsGoplsTestInstall(t)
	argsLog := filepath.Join(t.TempDir(), "gopls-timing-args.log")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := startWindowsGoplsMCPBinaryForTest(
		t, ctx, install.Binary, root, filepath.Dir(install.Gopls),
		windowsGoplsSidecarEnv(t, install, fakeGoplsArgsLogEnv+"="+argsLog, windowsFakeGoplsStrictFileURIEnv+"=1"),
	)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	pos := target + ":3:6"
	checks := []struct {
		name string
		tool string
		args map[string]any
	}{
		{name: "document_symbol", tool: "structure", args: map[string]any{"action": "document_symbol", "file_path": target}},
		{name: "hover", tool: "inspect", args: map[string]any{"action": "hover", "pos": pos}},
		{name: "definition", tool: "inspect", args: map[string]any{"action": "definition", "pos": pos}},
		{name: "references", tool: "xref", args: map[string]any{"action": "references", "pos": pos}},
	}
	for _, check := range checks {
		started := time.Now()
		result := client.callTool(t, check.tool, check.args)
		elapsed := time.Since(started)
		invocations, _ := os.ReadFile(argsLog)
		stderr := strings.TrimSpace(client.stderrString())
		t.Logf("action=%s elapsed=%s sidecar_pid=%d fake_gopls_invocations=%s sidecar_stderr=%s", check.name, elapsed, client.cmd.Process.Pid, strings.TrimSpace(string(invocations)), stderr)
		if result.Result.IsError {
			t.Fatalf("%s returned MCP error: text=%q structured=%s stderr=%s", check.name, result.Result.ContentText(), result.Result.StructuredContent, client.stderrString())
		}
	}
}

func TestLSPBinaryWindowsDriveFileURIReachesStrictServer(t *testing.T) {
	skipLSPBinaryResidualE2EInShortMode(t)
	root := canonicalToolTestRoot(t, t.TempDir())
	writeLSPBinaryFixture(t, filepath.Join(root, "go.mod"), "module example.com/windows-uri\n\ngo 1.26\n")
	target := filepath.Join(root, "main.go")
	writeLSPBinaryFixture(t, target, "package main\n\nfunc WindowsDriveURI() {}\n")
	install := buildWindowsGoplsTestInstall(t)
	argsLog := filepath.Join(t.TempDir(), "gopls-args.log")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client := startWindowsGoplsMCPBinaryForTest(
		t,
		ctx,
		install.Binary,
		root,
		filepath.Dir(install.Gopls),
		windowsGoplsSidecarEnv(
			t,
			install,
			fakeGoplsArgsLogEnv+"="+argsLog,
			windowsFakeGoplsStrictFileURIEnv+"=1",
		),
	)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	result := client.callTool(t, "structure", map[string]any{
		"action":    "document_symbol",
		"file_path": target,
	})
	if result.Result.IsError {
		t.Fatalf("strict Windows URI document_symbol returned tool error: %s; stderr=%s", result.Result.ContentText(), client.stderrString())
	}
	if !strings.Contains(result.Result.ContentText(), "WindowsDriveURI") {
		t.Fatalf("strict Windows URI document_symbol content = %s, want WindowsDriveURI; stderr=%s", result.Result.ContentText(), client.stderrString())
	}
}
