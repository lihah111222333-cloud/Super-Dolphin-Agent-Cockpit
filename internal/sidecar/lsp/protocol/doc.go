// Package protocol defines the JSON-RPC and LSP DTOs used by the mcp-lsp
// sidecar.
//
// The package owns protocol shapes, codec helpers, and notification dispatch
// contracts only. It must not import sidecar managers, tools, filesystem
// search, logging, or process lifecycle code.
package protocol
