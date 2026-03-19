package provider

// Capability represents a provider feature flag.
type Capability string

const (
	CapMessageSend    Capability = "message.send"
	CapMessageStream  Capability = "message.stream"
	CapToolUse        Capability = "tool.use"
	CapToolMCP        Capability = "tool.mcp"       // V3: all providers support MCP
	CapImageInput     Capability = "image.input"
	CapContextCompact Capability = "context.compact"
	CapThreadStart    Capability = "thread.start"
	CapThreadResume   Capability = "thread.resume"
	CapThreadFork     Capability = "thread.fork"
	CapThreadList     Capability = "thread.list"
)

// CapabilitySet defines what a provider supports.
type CapabilitySet map[Capability]bool

// ClaudeCapabilities returns the capability set for Claude provider.
func ClaudeCapabilities() CapabilitySet {
	return CapabilitySet{
		CapMessageSend:    true,
		CapMessageStream:  true,
		CapToolUse:        true,
		CapToolMCP:        true,
		CapImageInput:     true,
		CapContextCompact: true,
		CapThreadStart:    true,
		CapThreadResume:   true,
		CapThreadFork:     true,
		CapThreadList:     true,
	}
}

// CodexCapabilities returns the capability set for Codex provider.
func CodexCapabilities() CapabilitySet {
	return CapabilitySet{
		CapMessageSend:    true,
		CapMessageStream:  true,
		CapToolUse:        true,
		CapToolMCP:        true, // V3: Codex now supports MCP tools
		CapContextCompact: true,
		CapThreadStart:    true,
		CapThreadResume:   true,
		CapThreadFork:     false,
		CapThreadList:     true,
	}
}
