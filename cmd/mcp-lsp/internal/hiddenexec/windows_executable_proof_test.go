//go:build windows

package hiddenexec

import "testing"

func TestWindowsGoplsBrokerDeliveryNameAllowedRequiresExecutableExtension(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"mcp-lsp.exe",
		"mcp-lsp-windows-arm64.exe",
		"mcp-lsp-windows-x64.exe",
		"mcp-lsp-windows-x86.exe",
		"mcp-lsp-windows-arm64.rebuild-v2.exe",
		"renamed-lsp.exe",
		"MCP-LSP.EXE",
	} {
		if !windowsGoplsBrokerDeliveryNameAllowed(name) {
			t.Fatalf("delivery name %q was rejected", name)
		}
	}
	for _, name := range []string{
		"mcp-lsp",
		"mcp-lsp.cmd",
		"mcp-lsp.exe.bak",
		"",
	} {
		if windowsGoplsBrokerDeliveryNameAllowed(name) {
			t.Fatalf("non-executable delivery name %q was accepted", name)
		}
	}
}
