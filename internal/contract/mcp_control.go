package contract

import (
	"context"
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

// ToolInstance is a registry snapshot for a connected MCP peer.
type ToolInstance struct {
	Lease mcp.LeaseKey
	// Deprecated: use LeaseKey. Will be removed after 2026-06-30.
	LeaseID       string
	BinaryName    string
	AgentID       string
	ThreadID      string
	PID           int
	Capabilities  []string
	Subscriptions []string
	PeerKind      string
	ClientKind    string
	Status        string
	ConfigVersion int64
}

// ToolRegistry coordinates ctl/* peer registration and instance lifecycle.
type ToolRegistry interface {
	Register(ctx context.Context, req mcp.RegisterRequest) (mcp.RegisterResponse, error)
	Heartbeat(ctx context.Context, req mcp.HeartbeatRequest) (mcp.HeartbeatResponse, error)
	GetInstance(key mcp.LeaseKey) (ToolInstance, bool)
	ShutdownInstance(ctx context.Context, key mcp.LeaseKey, req mcp.ShutdownRequest) error
}

// ToolNotifier fans notifications out to connected peers.
type ToolNotifier interface {
	NotifyBySubscription(ctx context.Context, topic, method string, params any) error
	NotifyByCapability(ctx context.Context, capability, method string, params any) error
	NotifyBySelector(ctx context.Context, sel mcp.Selector, method string, params any) error
	NotifyConfigChanged(ctx context.Context, topic string, scope *mcp.SelectorScope, configVersion int64, payload json.RawMessage) error
}

// ToolHookCallback dispatches topic-based hook callbacks to subscribed peers.
type ToolHookCallback interface {
	CallbackHookBefore(ctx context.Context, topic string, payload mcp.HookPayload) error
	CallbackHookCheck(ctx context.Context, topic string, payload mcp.HookPayload) error
	CallbackHookAfter(ctx context.Context, topic string, payload mcp.HookPayload) error
}

// PeerCallback dispatches lease-targeted hook callbacks to a specific peer.
type PeerCallback interface {
	CallbackBefore(ctx context.Context, lease mcp.LeaseKey, payload mcp.HookPayload) (mcp.BeforeDecision, error)
	CallbackCheck(ctx context.Context, lease mcp.LeaseKey, payload mcp.HookPayload) (mcp.CheckDecision, error)
	CallbackAfter(ctx context.Context, lease mcp.LeaseKey, payload mcp.HookPayload) (mcp.AfterDecision, error)
}

// ToolControlPlane preserves the full registry surface for integrations that need it.
type ToolControlPlane interface {
	ToolRegistry
	ToolNotifier
	ToolHookCallback
	PeerCallback
}
