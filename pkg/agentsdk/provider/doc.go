// Package provider implements the unified provider abstraction for V3.
//
// Architecture Decision: ADR-001
// Both Claude and Codex use MCP for tool delivery. Provider-specific code
// is limited to transport (CLI for Claude, AppServer for Codex).
//
// Key types:
//   - UnifiedProvider: orchestrates launch, submit, resume via transport abstraction
//   - MCPBridge: generates MCP config for tool injection (shared by all providers)
//   - Transport: interface for provider-specific connection management
package provider
