package toolbridge

// P22 P4 S3b: freeze the toolbridge wire-protocol surface that was
// previously a scatter of magic strings across handler.go / proxy.go.
// P4 plan §94-106 lists this freeze as a prerequisite for S3d
// (import-direction collapse): consumers need named, testable
// invariants before the implementation files can be refactored or
// moved.
//
// Everything in this file is a CONTRACT (observed by wire peers +
// archtest guard) and must not drift without a schema version bump.
// Adding new entries is fine; changing an existing value is a protocol
// break.

// MetadataKeyAgentID, MetadataKeyThreadID, MetadataKeyCallID are the
// private metadata keys the bridge injects into every downstream
// `tools/call` payload so peer MCP servers can attribute a call back
// to the originating agent/thread/call. The leading underscore is
// load-bearing: it prevents collision with any tool-defined argument
// and telegraphs "internal, provider-agnostic" to peer implementors.
// These names are part of the peer contract — do not rename without
// coordinating every peer server.
const (
	MetadataKeyAgentID        = "_agentId"
	MetadataKeyThreadID       = "_threadId"
	MetadataKeyCallID         = "_callId"
	MetadataKeyCWD            = "_cwd"
	MetadataKeyWorkspaceRoots = "_workspaceRoots"
)

// ProxyProtocolVersion and ProxyServerInfo* are the fixed-value
// responses returned from the /mcp/{family}/{agentID} proxy's
// `initialize` method. They must round-trip stably across restarts so
// external MCP clients cache handshake responses deterministically.
const (
	ProxyProtocolVersion    = "2025-11-25"
	ProxyServerInfoName     = "proxy"
	ProxyServerInfoVersion  = "1.0.0"
	ProxyNotificationMethod = "notifications/initialized"
)

// Supported proxy JSON-RPC methods. The proxy dispatches on method
// name; anything not in this set is a method-not-found error
// (jsonRPCCodeMethodMiss). TestToolbridgeCompatibilityFallbackRemoved
// locks this — we do NOT silent-ACK unknown methods (§fallback /
// §fail-closed).
const (
	ProxyMethodInitialize = "initialize"
	ProxyMethodToolsList  = "tools/list"
	ProxyMethodToolsCall  = "tools/call"
)
