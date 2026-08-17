//go:build windows

package hiddenexec

import "testing"

func TestWindowsGoplsBrokerDeliveryNameAllowedUsesUnambiguousArchitectureNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"mcp-lsp.exe",
		"mcp-lsp-windows-arm64.exe",
		"mcp-lsp-windows-x64.exe",
		"mcp-lsp-windows-x86.exe",
		"MCP-LSP-WINDOWS-ARM64.EXE",
	} {
		if !windowsGoplsBrokerDeliveryNameAllowed(name) {
			t.Fatalf("delivery name %q was rejected", name)
		}
	}
	for _, name := range []string{
		"mcp-lsp-windows-arm.exe",
		"mcp-lsp-windows-amd64.exe",
		"mcp-lsp-windows-386.exe",
		"mcp-lsp-windows-x86.exe.bak",
		"other.exe",
	} {
		if windowsGoplsBrokerDeliveryNameAllowed(name) {
			t.Fatalf("ambiguous or non-delivery name %q was accepted", name)
		}
	}
}
