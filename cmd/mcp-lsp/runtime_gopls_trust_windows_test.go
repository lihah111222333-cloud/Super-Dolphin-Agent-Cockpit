//go:build windows

package main

import "testing"

func TestRuntimeServerWindowsSkillDeliveryNameAllowedUsesUnambiguousArchitectureNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"mcp-lsp-windows-arm64.exe",
		"mcp-lsp-windows-x64.exe",
		"mcp-lsp-windows-x86.exe",
		"MCP-LSP-WINDOWS-X64.EXE",
	} {
		if !runtimeServerWindowsSkillDeliveryNameAllowed(name) {
			t.Fatalf("skill delivery name %q was rejected", name)
		}
	}
	for _, name := range []string{
		"mcp-lsp.exe",
		"mcp-lsp-windows-arm.exe",
		"mcp-lsp-windows-amd64.exe",
		"mcp-lsp-windows-386.exe",
	} {
		if runtimeServerWindowsSkillDeliveryNameAllowed(name) {
			t.Fatalf("ambiguous or non-skill delivery name %q was accepted", name)
		}
	}
}
