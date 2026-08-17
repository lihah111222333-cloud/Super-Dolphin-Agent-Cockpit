//go:build windows

package main

// lspBinaryExecutableNameForTest 返回 Windows 测试产物名；windows build tag 明确
// 隔离 .exe 交付语义。
func lspBinaryExecutableNameForTest() string {
	return "mcp-lsp.exe"
}
