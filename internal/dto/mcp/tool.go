package mcp

import "encoding/json"

// MCPTool is the canonical tool schema exchanged between MCP peers.
// Defined in the DTO layer so any architecture layer can reference it
// without depending on protocol-specific packages.
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}
