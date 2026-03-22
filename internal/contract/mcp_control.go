package contract

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

// ToolInstance is a registry snapshot for a connected MCP peer.
type ToolInstance struct {
	Lease         mcp.LeaseKey
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

// ToolRegistry coordinates ctl/* peer registration and callbacks.
type ToolRegistry interface {
	Register(ctx context.Context, req mcp.RegisterRequest) (mcp.RegisterResponse, error)
	Heartbeat(ctx context.Context, req mcp.HeartbeatRequest) (mcp.HeartbeatResponse, error)
	GetInstance(key mcp.LeaseKey) (ToolInstance, bool)
	NotifyBySubscription(ctx context.Context, topic, method string, params any) error
	NotifyByCapability(ctx context.Context, capability, method string, params any) error
	ShutdownInstance(ctx context.Context, key mcp.LeaseKey, req mcp.ShutdownRequest) error
}
