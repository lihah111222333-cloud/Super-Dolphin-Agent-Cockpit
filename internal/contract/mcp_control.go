package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// MCPServerConfig 是工作区 MCP server 配置的跨模块 wire 形状。
// Enabled 为指针用于区分“未写入该字段”和“显式关闭”。
type MCPServerConfig struct {
	TrustedServerID string            `json:"trustedServerId,omitempty"`
	Transport       string            `json:"transport,omitempty"`
	URL             string            `json:"url,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Command         string            `json:"command,omitempty"`
	Args            []string          `json:"args,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	Enabled         *bool             `json:"enabled,omitempty"`
}

const RuntimeMCPTrustedServerIDKey = "trustedServerId"

const (
	runtimeMCPSQLitePackage       = "@bytebase/dbhub@0.23.0"
	runtimeMCPLegacySQLitePackage = "@modelcontextprotocol/server-sqlite"
	runtimeMCPBrokenSQLitePackage = "mcp-server-sqlite"
	runtimeMCPPlaywrightPackage   = "@playwright/mcp@latest"
)

// RuntimeMCPPolicy 集中校验 runtime MCP 配置的信任边界。
// thread/start 的开放 config 不能直接携带 command/url/header/env；只有 mcp_server 模块
// 产出的配置会带 trustedServerId，并在 provider/toolbridge 入口再次校验。
type RuntimeMCPPolicy struct{}

// DefaultRuntimeMCPPolicy 返回无状态策略实例，方便各包共享同一组错误语义。
func DefaultRuntimeMCPPolicy() RuntimeMCPPolicy {
	return RuntimeMCPPolicy{}
}

// RejectThreadStartConfig 拒绝开放 thread/start config 直接声明 MCP peer。
// 受控 MCP 配置只能来自 internal/module/mcp_server 读取后的 MCPSnapshot.ServerConfigs。
func (RuntimeMCPPolicy) RejectThreadStartConfig(cfg map[string]any) error {
	if len(cfg) == 0 {
		return nil
	}
	for _, key := range []string{"mcpConfig", "mcp_config"} {
		if _, ok := cfg[key]; ok {
			return fmt.Errorf("runtime MCP config %s must reference a trusted MCP server from mcp_server, not raw thread/start config", key)
		}
	}
	return nil
}

// ValidateRuntimeServerReference 校验单个 mcpConfig.mcpServers 条目是否来自受控 server id。
func (RuntimeMCPPolicy) ValidateRuntimeServerReference(name string, server map[string]any) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("runtime MCP server name is required")
	}
	raw, ok := server[RuntimeMCPTrustedServerIDKey]
	if !ok {
		return "", fmt.Errorf("runtime MCP server %q must include trusted server id", name)
	}
	serverID, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("runtime MCP server %q trusted server id must be a string", name)
	}
	serverID = strings.TrimSpace(serverID)
	if serverID == "" {
		return "", fmt.Errorf("runtime MCP server %q trusted server id is required", name)
	}
	if serverID != name {
		return "", fmt.Errorf("runtime MCP server %q trusted server id %q does not match server name", name, serverID)
	}
	return serverID, nil
}

// ValidateManifestBinary 校验 provider manifest 中的 MCP binary 是否处在受控来源和命令边界内。
// 内置 lsp/orch/ida peer 可以没有 trustedServerId，但 stdio 命令仍必须匹配固定 sidecar 名称。
func (RuntimeMCPPolicy) ValidateManifestBinary(binary providerdto.MCPBinary) error {
	name := strings.TrimSpace(binary.Name)
	if name == "" {
		return fmt.Errorf("runtime MCP binary name is required")
	}
	if strings.EqualFold(strings.TrimSpace(binary.Type), "http") || strings.TrimSpace(binary.URL) != "" {
		return nil
	}
	if IsManagedRuntimeMCPServerName(name) {
		return DefaultRuntimeMCPPolicy().validateManagedManifestBinary(name, binary)
	}
	return DefaultRuntimeMCPPolicy().validateTrustedManifestBinary(name, binary)
}

// validateManagedManifestBinary 校验内置 sidecar manifest 的 stdio 命令形态。
func (policy RuntimeMCPPolicy) validateManagedManifestBinary(name string, binary providerdto.MCPBinary) error {
	if len(binary.Command) == 0 {
		return nil
	}
	if err := policy.ValidateManagedRuntimeStdioCommand(name, binary.Command[0], binary.Command[1:]); err != nil {
		return fmt.Errorf("runtime MCP binary %q: %w", name, err)
	}
	return nil
}

// validateTrustedManifestBinary 校验外部 MCP server manifest 的 trustedServerId 和 stdio 白名单。
func (policy RuntimeMCPPolicy) validateTrustedManifestBinary(name string, binary providerdto.MCPBinary) error {
	serverID := strings.TrimSpace(binary.TrustedServerID)
	if serverID == "" {
		return fmt.Errorf("runtime MCP binary %q must include trusted server id", name)
	}
	if serverID != name {
		return fmt.Errorf("runtime MCP binary %q trusted server id %q does not match server name", name, serverID)
	}
	if len(binary.Command) > 0 {
		if err := policy.ValidateRuntimeStdioCommand(binary.Command[0], binary.Command[1:], ""); err != nil {
			return fmt.Errorf("runtime MCP binary %q: %w", name, err)
		}
	}
	return nil
}

// ValidateRuntimeStdioCommand 校验受控 MCP stdio argv 是否属于内置允许形态。
// trustedServerId 只证明配置来源，不能授权任意 command；路径化命令一律拒绝。
func (RuntimeMCPPolicy) ValidateRuntimeStdioCommand(command string, args []string, sqliteProductDBPath string) error {
	if !runtimeStdioCommandAllowed(command, args, sqliteProductDBPath) {
		return fmt.Errorf("unsupported stdio command %q", runtimeStdioCommandLabel(command))
	}
	return nil
}

// ValidateManagedRuntimeStdioCommand 校验内置 sidecar 的 manifest stdio 命令。
// 受管 peer 可使用绝对路径，但 basename 必须与 server name 一一绑定，且不能携带额外 argv。
func (RuntimeMCPPolicy) ValidateManagedRuntimeStdioCommand(name, command string, args []string) error {
	want, ok := managedRuntimeStdioCommandName(name)
	if !ok {
		return fmt.Errorf("unsupported managed stdio server %q", strings.TrimSpace(name))
	}
	if len(normalizeRuntimeStdioArgs(args)) != 0 {
		return fmt.Errorf("managed stdio command %q must not include args", want)
	}
	if runtimeStdioCommandBase(command) != want {
		return fmt.Errorf("managed stdio command %q must use %q", runtimeStdioCommandLabel(command), want)
	}
	return nil
}

func runtimeStdioCommandAllowed(command string, args []string, sqliteProductDBPath string) bool {
	command = strings.TrimSpace(command)
	if command == "" || runtimeStdioCommandHasPath(command) {
		return false
	}
	args = normalizeRuntimeStdioArgs(args)
	switch command {
	case "npx":
		return runtimeNPXArgsAllowed(args, sqliteProductDBPath)
	default:
		return false
	}
}

func runtimeNPXArgsAllowed(args []string, sqliteProductDBPath string) bool {
	switch {
	case slices.Equal(args, []string{runtimeMCPPlaywrightPackage}):
		return true
	case runtimeDefaultSQLiteArgsAllowed(args, sqliteProductDBPath):
		return true
	case runtimeLegacySQLiteArgsAllowed(args, sqliteProductDBPath):
		return true
	default:
		return false
	}
}

// runtimeDefaultSQLiteArgsAllowed 校验当前 dbhub SQLite 默认 stdio argv。
// 读取历史配置时 sqliteProductDBPath 可能为空，此时只接受非空 sqlite DSN 形态。
func runtimeDefaultSQLiteArgsAllowed(args []string, sqliteProductDBPath string) bool {
	if len(args) != 3 || args[0] != "-y" || args[1] != runtimeMCPSQLitePackage {
		return false
	}
	if sqliteProductDBPath == "" {
		return strings.HasPrefix(args[2], "--dsn=sqlite:///") && len(args[2]) > len("--dsn=sqlite:///")
	}
	return args[2] == "--dsn="+runtimeSQLiteDBHubDSN(sqliteProductDBPath)
}

// runtimeLegacySQLiteArgsAllowed 校验历史 SQLite MCP argv，并在有产品 DB 路径时绑定到该路径。
func runtimeLegacySQLiteArgsAllowed(args []string, sqliteProductDBPath string) bool {
	databasePath := runtimeLegacySQLiteDatabasePath(args)
	if databasePath == "" {
		return false
	}
	if sqliteProductDBPath == "" {
		return true
	}
	normalized, err := runtimeNormalizeSQLiteDatabasePath(databasePath)
	if err != nil {
		return false
	}
	return normalized == sqliteProductDBPath
}

// runtimeLegacySQLiteDatabasePath 提取历史 SQLite MCP server argv 中的数据库路径。
func runtimeLegacySQLiteDatabasePath(args []string) string {
	if len(args) == 3 && args[0] == "-y" && args[1] == runtimeMCPLegacySQLitePackage {
		return strings.TrimSpace(args[2])
	}
	if len(args) == 4 && args[0] == "-y" && args[1] == runtimeMCPBrokenSQLitePackage {
		switch args[2] {
		case "--db", "--database":
			return strings.TrimSpace(args[3])
		}
	}
	return ""
}

func runtimeSQLiteDBHubDSN(databasePath string) string {
	path := strings.TrimSpace(databasePath)
	if path == "" {
		return ""
	}
	return "sqlite:///" + filepath.ToSlash(path)
}

func runtimeNormalizeSQLiteDatabasePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("runtime MCP sqlite path: %w", err)
	}
	return filepath.Clean(abs), nil
}

func normalizeRuntimeStdioArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, 0, len(args))
	for _, arg := range args {
		out = append(out, strings.TrimSpace(arg))
	}
	return out
}

func runtimeStdioCommandHasPath(command string) bool {
	return strings.Contains(command, "/") || strings.Contains(command, `\`)
}

func runtimeStdioCommandBase(command string) string {
	command = strings.TrimSpace(command)
	if idx := strings.LastIndexAny(command, `/\`); idx >= 0 {
		command = command[idx+1:]
	}
	command = strings.ToLower(command)
	command = strings.TrimSuffix(strings.TrimSuffix(command, ".exe"), ".cmd")
	return command
}

func runtimeStdioCommandLabel(command string) string {
	command = strings.TrimSpace(command)
	if idx := strings.LastIndexAny(command, `/\`); idx >= 0 {
		command = command[idx+1:]
	}
	if command == "" {
		return "<empty>"
	}
	return command
}

func managedRuntimeStdioCommandName(name string) (string, bool) {
	switch strings.TrimSpace(name) {
	case string(providerdto.FamilyLSP):
		return "mcp-lsp", true
	case string(providerdto.FamilyOrch):
		return "mcp-orch", true
	case string(providerdto.FamilyIDA):
		return "mcp-ida", true
	default:
		return "", false
	}
}

// IsManagedRuntimeMCPServerName 判断 server 名称是否属于主进程生成的内置 peer。
func IsManagedRuntimeMCPServerName(name string) bool {
	switch strings.TrimSpace(name) {
	case string(providerdto.FamilyLSP), string(providerdto.FamilyOrch), string(providerdto.FamilyIDA):
		return true
	default:
		return false
	}
}

// MCPServerConfigProvider 读取指定工作区解析后的 MCP server 配置集合。
type MCPServerConfigProvider interface {
	ListMCPServerConfigs(ctx context.Context, cwd string) (map[string]MCPServerConfig, error)
}

// MCPToolAuthorityIssueRequest 请求 config owner 为一次 tools/list membership 签发 generation。
type MCPToolAuthorityIssueRequest struct {
	CWD              string
	Binary           providerdto.MCPBinary
	MembershipDigest string
}

// MCPToolAuthority 是 config owner 签发的 current generation 不可变令牌。
type MCPToolAuthority struct {
	CWD              string
	ServerID         string
	ConfigDigest     string
	MembershipDigest string
	Generation       uint64
	ConfigRevision   uint64
	Managed          bool
}

// MCPToolQuarantineCommit 绑定一次 current authority 与该 generation 的隔离集合。
type MCPToolQuarantineCommit struct {
	Authority MCPToolAuthority
	Tools     map[string]string
}

// MCPToolAuthorityOwner 持有 server current generation 与逐工具 quarantine。
// CompareAndSwapMCPToolQuarantines 在 owner 锁内复核 current 后执行 publish，
// 使 quarantine 写入和 surface 替换共享一个 CAS 边界。
type MCPToolAuthorityOwner interface {
	IssueMCPToolAuthority(context.Context, MCPToolAuthorityIssueRequest) (MCPToolAuthority, error)
	CheckMCPToolAuthority(context.Context, MCPToolAuthority) error
	WithMCPToolAuthority(context.Context, MCPToolAuthority, func() error) error
	CompareAndSwapMCPToolQuarantines(
		context.Context,
		[]MCPToolQuarantineCommit,
		func() error,
	) error
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

// MCPToolLifecycleState 表示 MCP tool 在当前 workspace/server 下的治理状态。
type MCPToolLifecycleState string

const (
	// MCPToolLifecycleEnabled 表示工具可被正常暴露和调用。
	MCPToolLifecycleEnabled MCPToolLifecycleState = "enabled"
	// MCPToolLifecycleDisabled 表示工具被人工关闭，后续可恢复。
	MCPToolLifecycleDisabled MCPToolLifecycleState = "disabled"
	// MCPToolLifecycleSuspended 表示工具被临时挂起，通常需要人工确认后恢复。
	MCPToolLifecycleSuspended MCPToolLifecycleState = "suspended"
	// MCPToolLifecycleRemoved 表示工具已从治理面移除，调用方必须拒绝使用。
	MCPToolLifecycleRemoved MCPToolLifecycleState = "removed"
)

const (
	// MCPToolLifecycleDenyCodeDisabled 标记工具因 disabled 状态被拒绝。
	MCPToolLifecycleDenyCodeDisabled = "mcp_tool_disabled"
	// MCPToolLifecycleDenyCodeSuspended 标记工具因 suspended 状态被拒绝。
	MCPToolLifecycleDenyCodeSuspended = "mcp_tool_suspended"
	// MCPToolLifecycleDenyCodeRemoved 标记工具因 removed 状态被拒绝。
	MCPToolLifecycleDenyCodeRemoved = "mcp_tool_removed"
	// MCPToolLifecycleDenyCodeServerDisabled 标记工具因所属 server 关闭被拒绝。
	MCPToolLifecycleDenyCodeServerDisabled = "mcp_server_disabled"
)

// MCPToolLifecycleDecision 返回调用 MCP tool 前需要读取的状态决策。
// 缺失记录应由实现返回错误，不能被调用方解释为默认放行。
type MCPToolLifecycleDecision struct {
	WorkspaceRoot   string                `json:"workspaceRoot"`
	ServerName      string                `json:"serverName"`
	ManifestName    string                `json:"manifestName,omitempty"`
	ToolName        string                `json:"toolName"`
	State           MCPToolLifecycleState `json:"state"`
	Reason          string                `json:"reason,omitempty"`
	ReplacementTool string                `json:"replacementTool,omitempty"`
	ServerDisabled  bool                  `json:"serverDisabled,omitempty"`
	DenyCode        string                `json:"denyCode,omitempty"`
	LastSeenAt      int64                 `json:"lastSeenAt"`
	CreatedAt       int64                 `json:"createdAt"`
	UpdatedAt       int64                 `json:"updatedAt"`
}

// MCPToolLifecycleObservedTool 是 tools/list 回填时记录的单个 MCP tool。
type MCPToolLifecycleObservedTool struct {
	ManifestName string `json:"manifestName,omitempty"`
	Name         string `json:"name"`
}

// StoreMCPToolLifecycleParams 是 store 写入 MCP tool lifecycle 的最小输入。
// State 必须显式传入，store 不负责把未知状态静默降级。
type StoreMCPToolLifecycleParams struct {
	WorkspaceRoot   string
	ServerName      string
	ManifestName    string
	ToolName        string
	State           MCPToolLifecycleState
	Reason          string
	ReplacementTool string
	NowMillis       int64
}

// BackfillMCPToolLifecycleParams 是 discovery 回填 MCP tool lifecycle 的输入。
// 回填只能刷新发现信息，不能覆盖人工设置的状态、原因或替代工具。
type BackfillMCPToolLifecycleParams struct {
	WorkspaceRoot string
	ServerName    string
	ManifestName  string
	ToolName      string
	NowMillis     int64
}

// MCPServerConfigStore 只暴露 MCP server 服务需要的配置持久化能力。
type MCPServerConfigStore interface {
	InsertServer(context.Context, StoreMCPServerConfigParams) (bool, error)
	ReplaceServer(context.Context, StoreMCPServerConfigParams) (bool, error)
	ListServers(context.Context, string) (map[string]MCPServerConfig, error)
	DeleteServer(context.Context, string, string) (bool, error)
	SetServerEnabled(context.Context, string, string, bool) (bool, error)
	GetToolLifecycle(context.Context, string, string, string) (MCPToolLifecycleDecision, error)
	ListToolLifecycle(context.Context, string, string) ([]MCPToolLifecycleDecision, error)
	ExportToolLifecycle(context.Context, string) ([]MCPToolLifecycleDecision, error)
	UpsertToolLifecycle(context.Context, StoreMCPToolLifecycleParams) (MCPToolLifecycleDecision, error)
	BackfillToolLifecycle(context.Context, BackfillMCPToolLifecycleParams) (MCPToolLifecycleDecision, error)
}

// MCPToolLifecyclePolicyRequest 描述调用前策略查询所需的 MCP tool 身份。
type MCPToolLifecyclePolicyRequest struct {
	WorkspaceRoot       string `json:"workspaceRoot,omitempty"`
	WorkspaceRootSource string `json:"workspaceRootSource,omitempty"`
	ServerName          string `json:"serverName"`
	ManifestName        string `json:"manifestName,omitempty"`
	ToolName            string `json:"toolName"`
	CallName            string `json:"callName,omitempty"`
}

// MCPToolLifecyclePolicyReader 暴露调用前只读策略入口。
type MCPToolLifecyclePolicyReader interface {
	ResolveMCPToolLifecycle(context.Context, MCPToolLifecyclePolicyRequest) (MCPToolLifecycleDecision, error)
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
