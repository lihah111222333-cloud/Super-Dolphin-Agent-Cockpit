//go:build windows && e2e

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryWindowsSkillDeliveryLayoutServesGoplsE2E(t *testing.T) {
	install := buildWindowsGoplsTestInstall(t)
	deliveryLSPRoot := filepath.Join(install.Root, "bin", "LSP")
	if err := os.MkdirAll(deliveryLSPRoot, 0o700); err != nil {
		t.Fatalf("create Windows skill delivery LSP root: %v", err)
	}
	if err := os.Rename(install.Binary, filepath.Join(deliveryLSPRoot, "mcp-lsp-windows-arm.exe")); err != nil {
		t.Fatalf("install mcp-lsp in Windows skill delivery layout: %v", err)
	}
	if err := os.Rename(install.Bundle, filepath.Join(deliveryLSPRoot, "lsp")); err != nil {
		t.Fatalf("install LSP bundle in Windows skill delivery layout: %v", err)
	}
	install.Binary = filepath.Join(deliveryLSPRoot, "mcp-lsp-windows-arm.exe")
	install.Bundle = filepath.Join(deliveryLSPRoot, "lsp")
	install.Manifest = filepath.Join(install.Bundle, "lsp-manifest.json")
	install.Gopls = filepath.Join(install.Bundle, "bin", "gopls.exe")

	root := t.TempDir()
	target := writeFakeGoplsGoFixture(t, root)
	argsLog := filepath.Join(t.TempDir(), "gopls-args.log")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := startWindowsGoplsMCPBinaryForTest(
		t,
		ctx,
		install.Binary,
		root,
		filepath.Dir(install.Gopls),
		windowsGoplsSidecarEnv(t, install, fakeGoplsArgsLogEnv+"="+argsLog),
	)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	result := client.callTool(t, "completion", map[string]any{"pos": target + ":3:1"})
	requireMCPToolSuccess(t, client, result, "Windows skill delivery layout gopls completion")
}
