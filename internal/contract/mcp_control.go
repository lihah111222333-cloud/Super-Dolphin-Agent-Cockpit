package contract

import (
	"context"
	"encoding/json"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

// MCPServerConfig 是工作区 MCP server 配置的跨模块 wire 形状。
// Enabled 为指针用于区分“未写入该字段”和“显式关闭”。
type MCPServerConfig struct {
	Transport string            `json:"transport,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Enabled   *bool             `json:"enabled,omitempty"`
}

// MCPServerConfigProvider 读取指定工作区解析后的 MCP server 配置集合。
type MCPServerConfigProvider interface {
	ListMCPServerConfigs(ctx context.Context, cwd string) (map[string]MCPServerConfig, error)
}

// MCPServerAddRequest 是跨模块写入 MCP server 配置的输入。
// 业务模块只提交标准 mcpServers map，具体文件合并和持久化由控制模块处理。
type MCPServerAddRequest struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// MCPServerAddResult 返回配置写入位置和本次写入的服务名列表。
type MCPServerAddResult struct {
	ConfigPath  string   `json:"configPath"`
	ServerNames []string `json:"serverNames"`
}

// MCPServerListResult 返回当前工作区解析到的 MCP server 配置集合和来源路径。
type MCPServerListResult struct {
	ConfigPath string                     `json:"configPath"`
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// MCPPostgresServerStartRequest 是默认 Postgres MCP server 显式启动入口的空请求。
type MCPPostgresServerStartRequest struct{}

// MCPPostgresServerStartResult 返回默认 Postgres MCP server 配置写入后的状态。
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
// DatabasePath 为空时由实现按当前工作区策略解析，非空时必须显式写入配置。
type MCPSQLiteServerStartRequest struct {
	DatabasePath string `json:"databasePath,omitempty"`
}

// MCPSQLiteServerStartResult 返回默认 SQLite MCP server 配置的写入和开启结果。
// Added 表示是否新增配置，Enabled 表示写入后是否处于启用状态。
type MCPSQLiteServerStartResult struct {
	ConfigPath string          `json:"configPath"`
	ServerName string          `json:"serverName"`
	Added      bool            `json:"added"`
	Enabled    bool            `json:"enabled"`
	Config     MCPServerConfig `json:"config"`
}

// MCPSQLiteServerStopRequest 是默认 SQLite MCP server 显式关闭入口的空请求。
type MCPSQLiteServerStopRequest struct{}

// MCPSQLiteServerStopResult 返回默认 SQLite MCP server 被关闭后的配置状态。
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

// MCPPlaywrightServerStartRequest 是默认 Playwright MCP server 显式启动入口的空请求。
type MCPPlaywrightServerStartRequest struct{}

// MCPPlaywrightServerStartResult 返回默认 Playwright MCP server 配置的写入和开启结果。
type MCPPlaywrightServerStartResult struct {
	ConfigPath string          `json:"configPath"`
	ServerName string          `json:"serverName"`
	Added      bool            `json:"added"`
	Enabled    bool            `json:"enabled"`
	Config     MCPServerConfig `json:"config"`
}

// MCPPlaywrightServerStopRequest 是默认 Playwright MCP server 显式关闭入口的空请求。
type MCPPlaywrightServerStopRequest struct{}

// MCPPlaywrightServerStopResult 返回默认 Playwright MCP server 被关闭后的配置状态。
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
// WorkspaceRoot+Name 定位一条配置，Config 保留完整 wire 结构供 store 持久化。
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

const (
	// MCPToolLifecycleStateActive 表示工具可展示、可调用。
	MCPToolLifecycleStateActive MCPToolLifecycleState = "active"
	// MCPToolLifecycleStateSuspended 表示工具被临时暂停，后续执行面必须拒绝直接调用。
	MCPToolLifecycleStateSuspended MCPToolLifecycleState = "suspended"
	// MCPToolLifecycleStateRemoved 表示用户移除的 tombstone，恢复必须显式写回 active。
	MCPToolLifecycleStateRemoved MCPToolLifecycleState = "removed"

	// MCPToolLifecycleSourceDiscovery 表示 lifecycle 行来自工具发现或 backfill。
	MCPToolLifecycleSourceDiscovery MCPToolLifecycleSource = "discovery"
	// MCPToolLifecycleSourceUser 表示 lifecycle 行来自用户显式操作。
	MCPToolLifecycleSourceUser MCPToolLifecycleSource = "user"
	// MCPToolLifecycleSourceMigration 表示 lifecycle 行来自历史数据迁移。
	MCPToolLifecycleSourceMigration MCPToolLifecycleSource = "migration"
	// MCPToolLifecycleSourceSystem 表示 lifecycle 行来自系统内部策略。
	MCPToolLifecycleSourceSystem MCPToolLifecycleSource = "system"
)

// MCPToolLifecycleState 是 MCP tool 的产品生命周期状态。
// 缺失记录不是合法状态；后续执行面必须在 backfill 后显式读取记录。
type MCPToolLifecycleState string

// MCPToolLifecycleSource 标记 lifecycle 行的写入来源，便于后续审计和冲突判断。
type MCPToolLifecycleSource string

// MCPToolLifecycleKey 使用原始 workspace/server/tool 三元组定位一条 lifecycle 行。
// ToolName 必须是 MCP tools/list 返回的原始名称，不是 toolbridge 派生别名。
type MCPToolLifecycleKey struct {
	WorkspaceRoot string `json:"workspaceRoot"`
	ServerName    string `json:"serverName"`
	ToolName      string `json:"toolName"`
}

// MCPToolLifecycleRecord 是跨模块读取 lifecycle 状态时使用的稳定 DTO。
type MCPToolLifecycleRecord struct {
	WorkspaceRoot string                 `json:"workspaceRoot"`
	ServerName    string                 `json:"serverName"`
	ToolName      string                 `json:"toolName"`
	State         MCPToolLifecycleState  `json:"state"`
	Reason        string                 `json:"reason"`
	Source        MCPToolLifecycleSource `json:"source"`
	UpdatedBy     string                 `json:"updatedBy"`
	CreatedAt     time.Time              `json:"createdAt"`
	UpdatedAt     time.Time              `json:"updatedAt"`
}

// MCPToolLifecycleUpsertParams 表示一次显式 lifecycle 写入。
type MCPToolLifecycleUpsertParams struct {
	Key       MCPToolLifecycleKey
	State     MCPToolLifecycleState
	Reason    string
	Source    MCPToolLifecycleSource
	UpdatedBy string
}

// MCPToolLifecycleDiscoveryParams 用于 discovery/backfill 只补缺失 active 行。
// 已存在的 suspended/removed 不能被发现流程覆盖。
type MCPToolLifecycleDiscoveryParams struct {
	Key       MCPToolLifecycleKey
	Reason    string
	UpdatedBy string
}

// MCPToolLifecycleListParams 限定按 workspace/server 列出 lifecycle 状态。
type MCPToolLifecycleListParams struct {
	WorkspaceRoot string
	ServerName    string
}

// MCPToolLifecycleReader 暴露 toolbridge 等只读消费者后续需要的 lifecycle 查询能力。
type MCPToolLifecycleReader interface {
	GetMCPToolLifecycleState(context.Context, MCPToolLifecycleKey) (MCPToolLifecycleRecord, error)
	ListMCPToolLifecycleStates(context.Context, MCPToolLifecycleListParams) ([]MCPToolLifecycleRecord, error)
}

// MCPToolLifecycleWriter 暴露 owner 模块写入和 backfill lifecycle 状态的最小能力。
type MCPToolLifecycleWriter interface {
	UpsertMCPToolLifecycleState(context.Context, MCPToolLifecycleUpsertParams) (MCPToolLifecycleRecord, error)
	EnsureDiscoveredMCPToolLifecycleState(context.Context, MCPToolLifecycleDiscoveryParams) (MCPToolLifecycleRecord, bool, error)
}

// MCPToolLifecycleStore 聚合 lifecycle 读写能力，由 mcp_server owner 模块消费。
type MCPToolLifecycleStore interface {
	MCPToolLifecycleReader
	MCPToolLifecycleWriter
}

// ToolInstance 是已连接 MCP peer 的 registry 快照。
// LeaseKey 是新控制面主键，LeaseID 仅为旧调用方读取保留。
type ToolInstance struct {
	Lease mcp.LeaseKey
	// Deprecated: use LeaseKey.
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

// ToolRegistry 维护 ctl/* peer 注册、心跳、查询和关闭生命周期。
type ToolRegistry interface {
	Register(ctx context.Context, req mcp.RegisterRequest) (mcp.RegisterResponse, error)
	Heartbeat(ctx context.Context, req mcp.HeartbeatRequest) (mcp.HeartbeatResponse, error)
	GetInstance(key mcp.LeaseKey) (ToolInstance, bool)
	ShutdownInstance(ctx context.Context, key mcp.LeaseKey, req mcp.ShutdownRequest) error
}

// ToolNotifier 按订阅、能力或 selector 把通知扇出给已连接 peer。
type ToolNotifier interface {
	NotifyBySubscription(ctx context.Context, topic, method string, params any) error
	NotifyByCapability(ctx context.Context, capability, method string, params any) error
	NotifyBySelector(ctx context.Context, sel mcp.Selector, method string, params any) error
	NotifyConfigChanged(ctx context.Context, topic string, scope *mcp.SelectorScope, configVersion int64, payload json.RawMessage) error
}

// ToolHookCallback 按 topic 把 hook 回调分发给订阅 peer。
type ToolHookCallback interface {
	CallbackHookBefore(ctx context.Context, topic string, payload mcp.HookPayload) error
	CallbackHookCheck(ctx context.Context, topic string, payload mcp.HookPayload) error
	CallbackHookAfter(ctx context.Context, topic string, payload mcp.HookPayload) error
}

// PeerCallback 按 lease key 把 hook 回调定向发送给单个 peer。
type PeerCallback interface {
	CallbackBefore(ctx context.Context, lease mcp.LeaseKey, payload mcp.HookPayload) (mcp.BeforeDecision, error)
	CallbackCheck(ctx context.Context, lease mcp.LeaseKey, payload mcp.HookPayload) (mcp.CheckDecision, error)
	CallbackAfter(ctx context.Context, lease mcp.LeaseKey, payload mcp.HookPayload) (mcp.AfterDecision, error)
}

// ToolControlPlane 合并注册、通知与 hook 回调端口，供需要完整控制面的集成层使用。
type ToolControlPlane interface {
	ToolRegistry
	ToolNotifier
	ToolHookCallback
	PeerCallback
}
