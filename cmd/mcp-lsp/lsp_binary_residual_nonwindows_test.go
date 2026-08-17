//go:build !windows

package main

// lspBinaryExecutableNameForTest 返回非 Windows 测试产物名。
func lspBinaryExecutableNameForTest() string {
	return "mcp-lsp"
}
