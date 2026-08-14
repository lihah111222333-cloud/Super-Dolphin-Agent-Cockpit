//go:build windows && e2e

package main

import (
	"context"
	"crypto/sha256"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// super-dolphin-ci: compile-group-exclusive
func TestWindowsLSPBundleRejectsTamperedGoplsSHA256E2E(t *testing.T) {
	install := buildWindowsGoplsTestInstall(t)
	writeWindowsGoplsManifest(t, install, strings.Repeat("0", sha256.Size*2))
	root := t.TempDir()
	target := writeFakeGoplsGoFixture(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(
		t, ctx, install.Binary, root, filepath.Dir(install.Gopls), windowsGoplsSidecarEnv(t, install),
	)
	t.Cleanup(func() { client.close(t) })
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	result := client.callTool(t, "completion", map[string]any{"pos": target + ":3:1"})
	if !result.Result.IsError || !strings.Contains(result.Result.ContentText(), "sha256 does not match") {
		t.Fatalf("tampered Windows gopls manifest was not rejected by the trusted gate: text=%q structured=%s stderr=%s",
			result.Result.ContentText(), result.Result.StructuredContent, client.stderrString())
	}
}
