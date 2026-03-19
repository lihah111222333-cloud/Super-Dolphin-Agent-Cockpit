package provider

import "context"

// Transport abstracts the provider-specific connection layer.
// Claude implements this via CLI spawn, Codex via AppServer connect.
type Transport interface {
	// Connect establishes connection to the provider backend.
	Connect(ctx context.Context, cfg ConnectConfig) error

	// Send delivers a message (prompt/tool_result) to the provider.
	Send(ctx context.Context, msg Message) error

	// Events returns a channel of provider events.
	Events() <-chan Event

	// Close terminates the connection.
	Close() error
}

// ConnectConfig holds unified launch parameters.
// Note: no DynamicTools field — tools are provided via MCPConfig.
type ConnectConfig struct {
	Prompt       string
	CWD          string
	Model        string
	Instructions string
	MCPConfig    string // Path to MCP config file (tools provided here)
	ThreadID     string // Empty for new thread, set for resume
}

// Message represents an outbound message to the provider.
type Message struct {
	Type    string // "prompt" | "tool_result"
	Content string
}

// Event represents an inbound event from the provider.
type Event struct {
	Type    string
	Payload map[string]any
}

// UnifiedProvider wraps a Transport with MCP bridge logic.
// This is the single provider implementation used by runner/AgentManager.
type UnifiedProvider struct {
	transport Transport
	bridge    *MCPBridge
}

// NewUnifiedProvider creates a provider with the given transport and MCP bridge.
func NewUnifiedProvider(t Transport, b *MCPBridge) *UnifiedProvider {
	return &UnifiedProvider{transport: t, bridge: b}
}

// Launch starts a new agent session.
// Tools are injected via MCP config, not via provider-specific params.
func (p *UnifiedProvider) Launch(ctx context.Context, cfg ConnectConfig) error {
	// 1. Generate MCP config from registered tools
	if cfg.MCPConfig == "" {
		mcpPath, err := p.bridge.GenerateConfig(cfg.CWD)
		if err != nil {
			return err
		}
		cfg.MCPConfig = mcpPath
	}

	// 2. Connect via transport (Claude CLI / Codex AppServer)
	return p.transport.Connect(ctx, cfg)
}
