// Package e2e provides end-to-end tests for provider startup wiring across
// both Claude CLI and Codex app-server providers.
//
// Tests in this package verify the current provider startup flow:
//   - Claude: MCPManifest -> JSON file -> --mcp-config
//   - Codex:  dynamic tool registry -> thread/start(dynamicTools)
//   - Provider-native skills: canonical mirrors are reconciled into
//     provider discovery roots before provider startup calls are made.
//
// Provider-native model selection remains a provider black box. Tests here
// verify Super-Dolphin startup wiring, not that a real Claude/Codex model will
// choose to invoke a mirrored skill in a particular prompt.
//
// Some tests require external tools (codex CLI) and are skipped
// when those tools are not available.
package e2e
