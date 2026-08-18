//go:build windows

package main

import "testing"

func TestRuntimeServerWindowsSkillDeliveryNameAllowedRequiresExecutableExtension(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"mcp-lsp-windows-arm64.exe",
		"mcp-lsp-windows-x64.exe",
		"mcp-lsp-windows-x86.exe",
		"mcp-lsp-windows-arm64.rebuild.exe",
		"renamed-lsp.exe",
		"MCP-LSP.EXE",
	} {
		if !runtimeServerWindowsSkillDeliveryNameAllowed(name) {
			t.Fatalf("skill delivery executable %q was rejected", name)
		}
	}
	for _, name := range []string{
		"mcp-lsp",
		"mcp-lsp.cmd",
		"mcp-lsp.exe.bak",
		"",
	} {
		if runtimeServerWindowsSkillDeliveryNameAllowed(name) {
			t.Fatalf("non-executable skill delivery name %q was accepted", name)
		}
	}
}
