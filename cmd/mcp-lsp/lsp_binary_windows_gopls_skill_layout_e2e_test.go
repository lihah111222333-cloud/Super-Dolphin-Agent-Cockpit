//go:build windows && e2e

package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryWindowsSkillDeliveryLayoutServesGoplsE2E(t *testing.T) {
	install := buildWindowsGoplsShortIdlePrecheckTestInstall(t)
	deliveryLSPRoot := filepath.Join(install.Root, "bin", "LSP")
	if err := os.MkdirAll(deliveryLSPRoot, 0o700); err != nil {
		t.Fatalf("create Windows skill delivery LSP root: %v", err)
	}
	deliveryBinary := filepath.Join(deliveryLSPRoot, "mcp-lsp-windows-"+windowsLSPDeliveryBinaryArchitectureForTest(t)+".exe")
	if err := os.Rename(install.Binary, deliveryBinary); err != nil {
		t.Fatalf("install mcp-lsp in Windows skill delivery layout: %v", err)
	}
	if err := os.Rename(install.Bundle, filepath.Join(deliveryLSPRoot, "lsp")); err != nil {
		t.Fatalf("install LSP bundle in Windows skill delivery layout: %v", err)
	}
	install.Binary = deliveryBinary
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

// windowsLSPDeliveryBinaryArchitectureForTest 把当前测试二进制的 Go 架构映射为交付文件名中的公开架构名。
// 本文件只在 windows && e2e 选源；未知架构必须失败，不能冒充任一受支持的 Windows 交付物。
func windowsLSPDeliveryBinaryArchitectureForTest(t *testing.T) string {
	t.Helper()
	switch runtime.GOARCH {
	case "arm64":
		return "arm64"
	case "amd64":
		return "x64"
	case "386":
		return "x86"
	default:
		t.Fatalf("unsupported Windows LSP delivery test architecture %q", runtime.GOARCH)
		return ""
	}
}
