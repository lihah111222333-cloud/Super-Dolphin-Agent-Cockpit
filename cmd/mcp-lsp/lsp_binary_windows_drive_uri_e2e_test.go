//go:build windows && e2e

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLSPBinaryWindowsDriveFileURIReachesStrictServer verifies that the real
// MCP binary sends RFC 8089 drive-letter URIs through the LSP initialize and
// document-symbol boundary.
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
