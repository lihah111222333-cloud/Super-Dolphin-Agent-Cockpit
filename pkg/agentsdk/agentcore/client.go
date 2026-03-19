// Package agentcore defines the core provider interface for V3.
//
// V3 changes from V2:
//   - Removed dynamicTools parameter from all methods (tools via MCP)
//   - Removed SendDynamicToolResult (MCP Server handles results)
//   - Simplified from 7 methods to 5
package agentcore

import "context"

// Client is the provider interface.
// V3: tools are always provided via MCP config, never injected as parameters.
type Client interface {
	// Launch starts a new agent session with MCP config for tools.
	Launch(ctx context.Context, cfg LaunchConfig) error

	// Submit sends a new prompt to the running agent.
	Submit(ctx context.Context, prompt string) error

	// Resume reconnects to an existing thread with MCP config for tools.
	Resume(ctx context.Context, cfg ResumeConfig) error

	// Compact triggers context compaction.
	Compact(ctx context.Context) error

	// Close terminates the provider connection.
	Close() error
}

// LaunchConfig holds parameters for starting a new session.
type LaunchConfig struct {
	Prompt       string
	CWD          string
	Model        string
	Instructions string
	MCPConfig    string // Path to MCP config (replaces dynamicTools)
}

// ResumeConfig holds parameters for resuming an existing session.
type ResumeConfig struct {
	ThreadID     string
	Prompt       string
	CWD          string
	Model        string
	Instructions string
	MCPConfig    string // Path to MCP config (replaces dynamicTools)
}
