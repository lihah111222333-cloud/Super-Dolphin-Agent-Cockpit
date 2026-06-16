// Package mcpwire owns low-level MCP JSON-RPC wire helpers.
//
// It provides transport framing and structured-content normalization used by
// MCP servers, sidecars, and toolbridge clients. It must not register tools,
// start services, call business modules, or own provider-specific behavior.
package mcpwire
