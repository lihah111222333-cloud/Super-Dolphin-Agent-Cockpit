// Package e2e provides end-to-end tests for MCP configuration injection
// across both Claude CLI and Codex app-server providers.
//
// Tests in this package verify the current provider startup flow:
//   - Claude: MCPManifest -> JSON file -> --mcp-config
//   - Codex:  dynamic tool registry -> thread/start(dynamicTools)
//
// Some tests require external tools (codex CLI) and are skipped
// when those tools are not available.
package e2e
