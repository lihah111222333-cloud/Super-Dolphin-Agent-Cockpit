package contract

import (
	"context"
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

type MCPServerConfig struct {
	Transport string            `json:"transport,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Enabled   *bool             `json:"enabled,omitempty"`
}

type MCPServerConfigProvider interface {
	ListMCPServerConfigs(ctx context.Context, cwd string) (map[string]MCPServerConfig, error)
}

// MCPServerAddRequest 是跨模块写入 MCP server 配置的输入，避免业务模块依赖具体实现。
type MCPServerAddRequest struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// MCPServerAddResult 返回 MCP server 配置写入位置和本次写入的服务名。
type MCPServerAddResult struct {
	ConfigPath  string   `json:"configPath"`
	ServerNames []string `json:"serverNames"`
}

// MCPServerListResult 返回当前工作区解析到的 MCP server 配置集合。
type MCPServerListResult struct {
	ConfigPath string                     `json:"configPath"`
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// MCPPostgresServerStartRequest 是默认 Postgres MCP server 显式启动入口的跨模块请求。
type MCPPostgresServerStartRequest struct{}

// MCPPostgresServerStartResult 返回默认 Postgres MCP server 配置的写入结果。
type MCPPostgresServerStartResult struct {
	ConfigPath string          `json:"configPath"`
	ServerName string          `json:"serverName"`
	Added      bool            `json:"added"`
	Config     MCPServerConfig `json:"config"`
}

// MCPPostgresServerStarter 暴露默认 Postgres MCP server 的启动能力，避免 module 之间直接依赖。
type MCPPostgresServerStarter interface {
	StartPostgresServer(context.Context, MCPPostgresServerStartRequest) (MCPPostgresServerStartResult, error)
}

// MCPSQLiteServerStartRequest 是默认 SQLite MCP server 显式启动入口的跨模块请求。
type MCPSQLiteServerStartRequest struct {
	DatabasePath string `json:"databasePath,omitempty"`
}

// MCPSQLiteServerStartResult 返回默认 SQLite MCP server 配置的写入和开启结果。
type MCPSQLiteServerStartResult struct {
	ConfigPath string          `json:"configPath"`
	ServerName string          `json:"serverName"`
	Added      bool            `json:"added"`
	Enabled    bool            `json:"enabled"`
	Config     MCPServerConfig `json:"config"`
}

// MCPSQLiteServerStopRequest 是默认 SQLite MCP server 显式关闭入口的跨模块请求。
type MCPSQLiteServerStopRequest struct{}

// MCPSQLiteServerStopResult 返回默认 SQLite MCP server 被关闭后的状态。
type MCPSQLiteServerStopResult struct {
	ConfigPath string `json:"configPath"`
	ServerName string `json:"serverName"`
	Enabled    bool   `json:"enabled"`
}

// MCPSQLiteServerController 暴露默认 SQLite MCP server 的开关能力。
type MCPSQLiteServerController interface {
	StartSQLiteServer(context.Context, MCPSQLiteServerStartRequest) (MCPSQLiteServerStartResult, error)
	StopSQLiteServer(context.Context, MCPSQLiteServerStopRequest) (MCPSQLiteServerStopResult, error)
}

// MCPPlaywrightServerStartRequest 是默认 Playwright MCP server 显式启动入口的跨模块请求。
type MCPPlaywrightServerStartRequest struct{}

// MCPPlaywrightServerStartResult 返回默认 Playwright MCP server 配置的写入和开启结果。
type MCPPlaywrightServerStartResult struct {
	ConfigPath string          `json:"configPath"`
	ServerName string          `json:"serverName"`
	Added      bool            `json:"added"`
	Enabled    bool            `json:"enabled"`
	Config     MCPServerConfig `json:"config"`
}

// MCPPlaywrightServerStopRequest 是默认 Playwright MCP server 显式关闭入口的跨模块请求。
type MCPPlaywrightServerStopRequest struct{}

// MCPPlaywrightServerStopResult 返回默认 Playwright MCP server 被关闭后的状态。
type MCPPlaywrightServerStopResult struct {
	ConfigPath string `json:"configPath"`
	ServerName string `json:"serverName"`
	Enabled    bool   `json:"enabled"`
}

// MCPPlaywrightServerController 暴露默认 Playwright MCP server 的开关能力。
type MCPPlaywrightServerController interface {
	StartPlaywrightServer(context.Context, MCPPlaywrightServerStartRequest) (MCPPlaywrightServerStartResult, error)
	StopPlaywrightServer(context.Context, MCPPlaywrightServerStopRequest) (MCPPlaywrightServerStopResult, error)
}

// MCPServerConfigWriter 暴露默认 MCP server 启动入口需要的最小配置读写能力。
type MCPServerConfigWriter interface {
	AddServers(context.Context, MCPServerAddRequest) (MCPServerAddResult, error)
	ListServers(context.Context) (MCPServerListResult, error)
}

// StoreMCPServerConfigParams 是写入 MCP server 配置表的最小输入。
type StoreMCPServerConfigParams struct {
	WorkspaceRoot string
	Name          string
	Config        MCPServerConfig
}

// MCPServerConfigStore 只暴露 MCP server 服务需要的配置持久化能力。
type MCPServerConfigStore interface {
	InsertServer(context.Context, StoreMCPServerConfigParams) (bool, error)
	ListServers(context.Context, string) (map[string]MCPServerConfig, error)
	DeleteServer(context.Context, string, string) (bool, error)
	SetServerEnabled(context.Context, string, string, bool) (bool, error)
}

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
	Shared        bool
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
