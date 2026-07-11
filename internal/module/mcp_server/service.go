package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/httpegress"
)

var (
	errMissingMCPServers           = errors.New("mcp_server: mcpServers is required")
	errMissingServerName           = errors.New("mcp_server: server name is required")
	errDuplicateServerName         = errors.New("mcp_server: duplicate server name")
	errMissingServerTransport      = errors.New("mcp_server: transport is required")
	errUnsupportedTransport        = errors.New("mcp_server: unsupported transport")
	errMissingServerURL            = errors.New("mcp_server: url is required")
	errInvalidServerURL            = errors.New("mcp_server: invalid url")
	errMissingServerCommand        = errors.New("mcp_server: command is required")
	errUnsupportedStdioCommand     = errors.New("mcp_server: unsupported stdio command")
	errMissingServerArg            = errors.New("mcp_server: arg is required")
	errMissingServerEnvName        = errors.New("mcp_server: env name is required")
	errMissingServerEnvValue       = errors.New("mcp_server: env value is required")
	errMissingHeaderName           = errors.New("mcp_server: header name is required")
	errMissingHeaderValue          = errors.New("mcp_server: header value is required")
	errInvalidConfigDocument       = errors.New("mcp_server: invalid config document")
	errServerAlreadyExists         = errors.New("mcp_server: server already exists")
	errServerNotFound              = errors.New("mcp_server: server not found")
	errMCPServerStoreNotConfigured = errors.New("mcp_server: config store is not configured")
	errMCPServerToolsRequestFailed = errors.New("mcp_server: tools request failed")
	errInvalidToolsResponse        = errors.New("mcp_server: invalid tools response")
	errPostgresInstallerMissing    = errors.New("mcp_server: postgres installer is not configured")
	errMissingToolName             = errors.New("mcp_server: tool name is required")
	errInvalidToolLifecycleState   = errors.New("mcp_server: invalid tool lifecycle state")
	errToolLifecycleNotFound       = errors.New("mcp_server: tool lifecycle not found")
)

// Service 定义 MCP server 配置管理的跨模块入口。
// 该接口只读写配置和执行 HTTP tools/list 探测，不直接启动 stdio/http MCP 进程。
type Service interface {
	AddServers(context.Context, AddServersRequest) (AddServersResult, error)
	ListServers(context.Context) (ListServersResult, error)
	ListServersForCWD(context.Context, string) (ListServersResult, error)
	ListServerTools(context.Context, ListServerToolsRequest) (ListServerToolsResult, error)
	StartPostgresServer(context.Context, StartPostgresServerRequest) (StartPostgresServerResult, error)
	StartSQLiteServer(context.Context, StartSQLiteServerRequest) (StartSQLiteServerResult, error)
	StopSQLiteServer(context.Context, StopSQLiteServerRequest) (StopSQLiteServerResult, error)
	StartPlaywrightServer(context.Context, StartPlaywrightServerRequest) (StartPlaywrightServerResult, error)
	StopPlaywrightServer(context.Context, StopPlaywrightServerRequest) (StopPlaywrightServerResult, error)
	DeleteServer(context.Context, DeleteServerRequest) (DeleteServerResult, error)
	BackfillMCPServerTools(context.Context, BackfillMCPServerToolsRequest) ([]contract.MCPToolLifecycleDecision, error)
	SetMCPToolLifecycle(context.Context, SetMCPToolLifecycleRequest) (contract.MCPToolLifecycleDecision, error)
	ListMCPToolLifecycle(context.Context, ListMCPToolLifecycleRequest) ([]contract.MCPToolLifecycleDecision, error)
	ExportMCPToolLifecycle(context.Context, ExportMCPToolLifecycleRequest) ([]contract.MCPToolLifecycleDecision, error)
	ResolveMCPToolLifecycle(context.Context, contract.MCPToolLifecyclePolicyRequest) (contract.MCPToolLifecycleDecision, error)
}

// ConfigDocument 是落盘配置文件的 JSON 外壳。
// mcpServers 字段保持与 provider 原生配置兼容，内部读写前会复制为 store DTO。
type ConfigDocument struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

// AddServersRequest 是前端和 RPC 传入的新增 MCP server 配置请求。
type AddServersRequest = contract.MCPServerAddRequest

// DeleteServerRequest 指定当前 workspace 下要删除的 MCP server 名称。
type DeleteServerRequest struct {
	ServerName string `json:"serverName"`
}

// ServerConfig 是跨模块共享的 MCP server 配置 wire 类型。
type ServerConfig = contract.MCPServerConfig

// AddServersResult 返回新增配置的落盘路径和按名称排序后的 server 列表。
type AddServersResult = contract.MCPServerAddResult

// ListServersResult 返回 workspace 配置路径和已规范化的 server 映射。
type ListServersResult = contract.MCPServerListResult

// ListServerToolsRequest 指定要通过 HTTP MCP tools/list 拉取工具的服务端名称。
type ListServerToolsRequest struct {
	ServerName string `json:"serverName"`
}

// ListServerToolsResult 返回远端 MCP server 暴露的工具列表。
type ListServerToolsResult struct {
	ConfigPath string           `json:"configPath"`
	ServerName string           `json:"serverName"`
	Tools      []mcpdto.MCPTool `json:"tools"`
}

// DeleteServerResult 返回被删除的 server 名称和配置路径。
// Deleted 只在 store 确认删除成功后为 true，名称不存在会走错误路径。
type DeleteServerResult struct {
	ConfigPath string `json:"configPath"`
	ServerName string `json:"serverName"`
	Deleted    bool   `json:"deleted"`
}

// service 是 MCP server 配置管理的实现。
// store 负责持久化，httpClient 只用于 HTTP transport 的 tools/list 探测，installer 只服务默认 postgres 写入。
type service struct {
	store             MCPServerConfigStore
	httpClient        mcpHTTPDoer
	postgresInstaller postgresInstaller
	sqlitePath        string
}

// NewService 创建未绑定持久化 store 的 MCP server 服务。
// 该构造仅用于不需要写配置的装配路径；调用写接口会 fail-fast。
func NewService() Service {
	return NewServiceWithStore(nil)
}

// NewServiceWithStore 创建带配置 store 的 MCP server 服务。
// 默认 postgres installer 会被注入，SQLite 路径仍从请求或环境变量解析。
func NewServiceWithStore(store MCPServerConfigStore) Service {
	return newServiceWithStoreInstallerAndSQLitePath(store, newNPMPostgresInstaller(), "")
}

// NewServiceWithStoreAndConfig 创建带运行时配置的 MCP server 服务，用于解析默认 SQLite MCP server 路径。
func NewServiceWithStoreAndConfig(store MCPServerConfigStore, cfg *platformconfig.Config) Service {
	sqlitePath := ""
	if cfg != nil {
		sqlitePath = cfg.SQLitePath
	}
	return newServiceWithStoreInstallerAndSQLitePath(store, newNPMPostgresInstaller(), sqlitePath)
}

// newServiceWithStoreAndInstaller 创建带测试 installer 的服务实例。
// 该入口只给同包测试注入 installer，生产代码应使用公开构造函数。
func newServiceWithStoreAndInstaller(store MCPServerConfigStore, installer postgresInstaller) *service {
	return newServiceWithStoreInstallerAndSQLitePath(store, installer, "")
}

// newServiceWithStoreInstallerAndSQLitePath 汇总所有可注入依赖。
// sqlitePath 会在构造时 trim，后续 StartSQLiteServer 按请求、构造路径和环境变量顺序解析。
func newServiceWithStoreInstallerAndSQLitePath(store MCPServerConfigStore, installer postgresInstaller, sqlitePath string) *service {
	return &service{store: store, httpClient: defaultMCPHTTPClient, postgresInstaller: installer, sqlitePath: strings.TrimSpace(sqlitePath)}
}

// AddServers 校验并写入当前 workspace 的 MCP server 配置。
// 名称、transport、HTTP URL、stdio command/env 都会先规范化；同名 server 已存在时直接报错。
func (s *service) AddServers(ctx context.Context, req AddServersRequest) (AddServersResult, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return AddServersResult{}, err
	}
	sqliteProductDBPath, err := s.sqliteProductDBPathForMCPServers(req.MCPServers)
	if err != nil {
		return AddServersResult{}, err
	}
	additions, names, err := normalizeMCPServers(req.MCPServers, sqliteProductDBPath)
	if err != nil {
		return AddServersResult{}, err
	}
	store, err := s.requireStore()
	if err != nil {
		return AddServersResult{}, err
	}

	workspaceRoot, err := currentMCPServerWorkspaceRoot()
	if err != nil {
		return AddServersResult{}, err
	}
	configPath := mcpServerConfigPath(workspaceRoot)
	existing, err := store.ListServers(ctx, workspaceRoot)
	if err != nil {
		return AddServersResult{}, err
	}
	if err := rejectExistingMCPServers(existing, names); err != nil {
		return AddServersResult{}, err
	}
	if err := insertMCPServerConfigs(ctx, store, workspaceRoot, additions, names); err != nil {
		return AddServersResult{}, err
	}

	return AddServersResult{ConfigPath: configPath, ServerNames: names}, nil
}

// sqliteProductDBPathForMCPServers 仅在新增配置包含 sqlite npx 包时解析产品数据库路径。
// 这样普通 HTTP/Playwright/Postgres 配置不依赖 sqlite 环境，但 dbhub argv 不能指向任意文件。
func (s *service) sqliteProductDBPathForMCPServers(input map[string]ServerConfig) (string, error) {
	if !mcpServersContainSQLiteNPXPackage(input) {
		return "", nil
	}
	if s == nil {
		return "", errMCPServerStoreNotConfigured
	}
	return s.resolveSQLiteDatabasePath("")
}

// ListServers 读取当前进程 workspace 的 MCP server 配置。
// workspace 解析失败或 store 缺失都会返回错误，不回退到空配置。
func (s *service) ListServers(ctx context.Context) (ListServersResult, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return ListServersResult{}, err
	}

	workspaceRoot, err := currentMCPServerWorkspaceRoot()
	if err != nil {
		return ListServersResult{}, err
	}
	return s.ListServersForCWD(ctx, workspaceRoot)
}

// ListServersForCWD 读取指定 cwd 对应 workspace 的 MCP server 配置。
// 返回的 map 是副本，调用方修改结果不会污染 store 缓存。
func (s *service) ListServersForCWD(ctx context.Context, cwd string) (ListServersResult, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return ListServersResult{}, err
	}
	workspaceRoot, servers, err := s.resolveWorkspaceServers(ctx, cwd)
	if err != nil {
		return ListServersResult{}, err
	}
	return ListServersResult{
		ConfigPath: mcpServerConfigPath(workspaceRoot),
		MCPServers: cloneMCPServers(servers),
	}, nil
}

// ListServerTools 通过已保存的 HTTP MCP server 配置执行 tools/list。
func (s *service) ListServerTools(ctx context.Context, req ListServerToolsRequest) (ListServerToolsResult, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return ListServerToolsResult{}, err
	}
	name, err := normalizeListServerToolsName(req)
	if err != nil {
		return ListServerToolsResult{}, err
	}
	workspaceRoot, servers, err := s.resolveWorkspaceServers(ctx, "")
	if err != nil {
		return ListServerToolsResult{}, err
	}
	config, ok := servers[name]
	if !ok {
		return ListServerToolsResult{}, fmt.Errorf("%w: %s", errServerNotFound, name)
	}
	config, err = normalizeServerConfig(name, config, "")
	if err != nil {
		return ListServerToolsResult{}, err
	}
	if !strings.EqualFold(config.Transport, "http") {
		return ListServerToolsResult{}, fmt.Errorf("%w: %s", errUnsupportedTransport, config.Transport)
	}
	tools, err := requestMCPServerHTTPTools(ctx, s.mcpHTTPClient(), config)
	if err != nil {
		return ListServerToolsResult{}, err
	}
	if tools == nil {
		tools = []mcpdto.MCPTool{}
	}
	if _, err := s.BackfillMCPServerTools(ctx, BackfillMCPServerToolsRequest{
		WorkspaceRoot: workspaceRoot,
		ServerName:    name,
		Tools:         observedMCPServerTools(tools, ""),
	}); err != nil {
		return ListServerToolsResult{}, fmt.Errorf("mcp_server: backfill discovered tools: %w", err)
	}
	return ListServerToolsResult{
		ConfigPath: mcpServerConfigPath(workspaceRoot),
		ServerName: name,
		Tools:      tools,
	}, nil
}

// DeleteServer 从当前工作区配置中删除指定 MCP server，名称不存在时直接返回错误避免静默成功。
func (s *service) DeleteServer(ctx context.Context, req DeleteServerRequest) (DeleteServerResult, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return DeleteServerResult{}, err
	}
	name, err := normalizeDeleteServerName(req)
	if err != nil {
		return DeleteServerResult{}, err
	}
	workspaceRoot, servers, err := s.resolveWorkspaceServers(ctx, "")
	if err != nil {
		return DeleteServerResult{}, err
	}
	if _, ok := servers[name]; !ok {
		return DeleteServerResult{}, fmt.Errorf("%w: %s", errServerNotFound, name)
	}
	store, err := s.requireStore()
	if err != nil {
		return DeleteServerResult{}, err
	}
	deleted, err := store.DeleteServer(ctx, workspaceRoot, name)
	if err != nil {
		return DeleteServerResult{}, err
	}
	if !deleted {
		return DeleteServerResult{}, fmt.Errorf("%w: %s", errServerNotFound, name)
	}
	return DeleteServerResult{
		ConfigPath: mcpServerConfigPath(workspaceRoot),
		ServerName: name,
		Deleted:    true,
	}, nil
}

type mcpServerConfigProvider struct {
	svc Service
}

// AsMCPServerConfigProvider 将 Service 适配为 provider 层可消费的 MCPServerConfigProvider。
// 适配器只暴露 enabled 配置，避免 provider 拉起已关闭的 server。
func AsMCPServerConfigProvider(svc Service) contract.MCPServerConfigProvider {
	return mcpServerConfigProvider{svc: svc}
}

// ListMCPServerConfigs 返回指定 cwd 下 provider 可启动的 MCP server 配置。
// service 缺失是装配错误，会直接返回 error，避免 provider 静默无 MCP 工具。
func (p mcpServerConfigProvider) ListMCPServerConfigs(ctx context.Context, cwd string) (map[string]contract.MCPServerConfig, error) {
	if p.svc == nil {
		return nil, errors.New("mcp server service is not configured")
	}
	result, err := p.svc.ListServersForCWD(ctx, cwd)
	if err != nil {
		return nil, err
	}
	return enabledMCPServersToContract(result.MCPServers), nil
}

// rejectExistingMCPServers 检查新增 server 是否和已有配置重名。
func rejectExistingMCPServers(existing map[string]ServerConfig, names []string) error {
	for _, name := range names {
		if _, ok := existing[name]; ok {
			return fmt.Errorf("%w: %s", errServerAlreadyExists, name)
		}
	}
	return nil
}

// normalizeListServerToolsName 校验 tools/list 请求中的 server 名称。
// 名称必须无首尾空白，避免 RPC 层和 store 层对同一个名字出现两种表示。
func normalizeListServerToolsName(req ListServerToolsRequest) (string, error) {
	name := strings.TrimSpace(req.ServerName)
	if name == "" || name != req.ServerName {
		return "", errMissingServerName
	}
	return name, nil
}

func observedMCPServerTools(tools []mcpdto.MCPTool, manifestName string) []contract.MCPToolLifecycleObservedTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]contract.MCPToolLifecycleObservedTool, 0, len(tools))
	for _, tool := range tools {
		out = append(out, contract.MCPToolLifecycleObservedTool{
			ManifestName: manifestName,
			Name:         tool.Name,
		})
	}
	return out
}

// insertMCPServerConfigs 按排序后的名称逐个写入新增配置。
// 任一 InsertServer 返回 false 都视为并发重名冲突，调用方应整体返回错误。
func insertMCPServerConfigs(
	ctx context.Context,
	store MCPServerConfigStore,
	workspaceRoot string,
	additions map[string]ServerConfig,
	names []string,
) error {
	for _, name := range names {
		inserted, err := store.InsertServer(ctx, StoreMCPServerConfigParams{
			WorkspaceRoot: workspaceRoot,
			Name:          name,
			Config:        additions[name],
		})
		if err != nil {
			return err
		}
		if !inserted {
			return fmt.Errorf("%w: %s", errServerAlreadyExists, name)
		}
	}
	return nil
}

// mcpHTTPClient 返回用于 HTTP MCP tools/list 的客户端。
// 服务未注入自定义 client 时使用带超时的默认 client。
func (s *service) mcpHTTPClient() mcpHTTPDoer {
	if s != nil && s.httpClient != nil {
		return s.httpClient
	}
	return defaultMCPHTTPClient
}

// requireStore 确认 MCP server 配置 store 已注入。
// 写入和读取配置都必须走该检查，避免 nil store 被解释成空配置。
func (s *service) requireStore() (MCPServerConfigStore, error) {
	if s == nil || s.store == nil {
		return nil, errMCPServerStoreNotConfigured
	}
	return s.store, nil
}

// resolveWorkspaceServers 读取当前工作区及兼容路径下的 MCP server 配置。
func (s *service) resolveWorkspaceServers(ctx context.Context, cwd string) (string, map[string]ServerConfig, error) {
	store, err := s.requireStore()
	if err != nil {
		return "", nil, err
	}
	workspaceRoot, err := normalizeMCPServerWorkspaceRoot(cwd)
	if err != nil {
		return "", nil, err
	}
	empty := map[string]ServerConfig{}
	for _, candidate := range mcpServerWorkspaceCandidates(workspaceRoot) {
		servers, err := store.ListServers(ctx, candidate)
		if err != nil {
			return "", nil, err
		}
		if len(servers) > 0 {
			return candidate, servers, nil
		}
		if candidate == workspaceRoot {
			empty = servers
		}
	}
	return workspaceRoot, empty, nil
}

func normalizeDeleteServerName(req DeleteServerRequest) (string, error) {
	name := strings.TrimSpace(req.ServerName)
	if name == "" || name != req.ServerName {
		return "", errMissingServerName
	}
	return name, nil
}

func mcpServerContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// normalizeMCPServers 校验并整理待写入的 MCP server 配置。
func normalizeMCPServers(
	input map[string]ServerConfig,
	sqliteProductDBPath string,
) (map[string]ServerConfig, []string, error) {
	if len(input) == 0 {
		return nil, nil, errMissingMCPServers
	}
	servers := make(map[string]ServerConfig, len(input))
	names := make([]string, 0, len(input))
	for rawName, rawConfig := range input {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, nil, errMissingServerName
		}
		if name != rawName {
			return nil, nil, fmt.Errorf("%w: %q", errMissingServerName, rawName)
		}
		if _, ok := servers[name]; ok {
			return nil, nil, fmt.Errorf("%w: %s", errDuplicateServerName, name)
		}
		config, err := normalizeServerConfig(name, rawConfig, sqliteProductDBPath)
		if err != nil {
			return nil, nil, err
		}
		servers[name] = config
		names = append(names, name)
	}
	sort.Strings(names)
	return servers, names, nil
}

// normalizeServerConfig 规范化 MCP server 配置，按 transport 分别校验 HTTP 与 stdio 字段。
func normalizeServerConfig(name string, config ServerConfig, sqliteProductDBPath string) (ServerConfig, error) {
	transport := strings.TrimSpace(config.Transport)
	if transport == "" {
		return ServerConfig{}, fmt.Errorf("%w: %s", errMissingServerTransport, name)
	}
	switch strings.ToLower(transport) {
	case "http":
		return normalizeHTTPServerConfig(name, config)
	case "stdio":
		return normalizeStdioServerConfig(name, config, sqliteProductDBPath)
	default:
		return ServerConfig{}, fmt.Errorf("%w: %s", errUnsupportedTransport, transport)
	}
}

func normalizeHTTPServerConfig(name string, config ServerConfig) (ServerConfig, error) {
	rawURL := strings.TrimSpace(config.URL)
	if rawURL == "" {
		return ServerConfig{}, fmt.Errorf("%w: %s", errMissingServerURL, name)
	}
	if err := validateHTTPURL(rawURL); err != nil {
		return ServerConfig{}, fmt.Errorf("%w: %s", err, rawURL)
	}

	headers, err := normalizeHeaders(name, config.Headers)
	if err != nil {
		return ServerConfig{}, err
	}
	return ServerConfig{
		Transport: "http",
		URL:       rawURL,
		Headers:   headers,
		Enabled:   normalizeServerEnabled(config.Enabled),
	}, nil
}

func normalizeStdioServerConfig(name string, config ServerConfig, sqliteProductDBPath string) (ServerConfig, error) {
	command := strings.TrimSpace(config.Command)
	if command == "" {
		return ServerConfig{}, fmt.Errorf("%w: %s", errMissingServerCommand, name)
	}
	args, err := normalizeStringList(config.Args, errMissingServerArg, name)
	if err != nil {
		return ServerConfig{}, err
	}
	if !allowedStdioServerCommand(command, args, sqliteProductDBPath) {
		return ServerConfig{}, fmt.Errorf("%w: %s", errUnsupportedStdioCommand, name)
	}
	env, err := normalizeStringMap(config.Env, errMissingServerEnvName, errMissingServerEnvValue, name)
	if err != nil {
		return ServerConfig{}, err
	}
	return ServerConfig{
		Transport: "stdio",
		Command:   command,
		Args:      args,
		Env:       env,
		Enabled:   normalizeServerEnabled(config.Enabled),
	}, nil
}

func allowedStdioServerCommand(command string, args []string, sqliteProductDBPath string) bool {
	return contract.DefaultRuntimeMCPPolicy().ValidateRuntimeStdioCommand(command, args, sqliteProductDBPath) == nil
}

// allowedNPXServerArgs 只接受项目内置 MCP server 的完整 argv，避免在包名后追加任意参数绕过 stdio 边界。
func allowedNPXServerArgs(args []string, sqliteProductDBPath string) bool {
	switch {
	case slices.Equal(args, []string{defaultPlaywrightPackage}):
		return true
	case slices.Equal(args, []string{"-y", defaultPostgresPackage, defaultPostgresDatabaseURL}):
		return true
	case isDefaultSQLiteNPXArgs(args, sqliteProductDBPath):
		return true
	case isLegacySQLiteNPXArgs(args, sqliteProductDBPath):
		return true
	default:
		return false
	}
}

// isDefaultSQLiteNPXArgs 只识别当前 dbhub 默认启动形态，dsn 内容由 SQLite 启动入口负责固定来源。
func isDefaultSQLiteNPXArgs(args []string, sqliteProductDBPath string) bool {
	return len(args) == 3 &&
		args[0] == "-y" &&
		args[1] == defaultSQLitePackage &&
		sqliteProductDBPath != "" &&
		args[2] == "--dsn="+sqliteDBHubDSN(sqliteProductDBPath)
}

// isLegacySQLiteNPXArgs 只放行可迁移的旧 sqlite 默认形态，防止读取历史配置时被任意 npx 参数污染。
func isLegacySQLiteNPXArgs(args []string, sqliteProductDBPath string) bool {
	databasePath := legacySQLiteDatabasePath(args)
	if databasePath == "" || sqliteProductDBPath == "" {
		return false
	}
	normalized, err := normalizeSQLiteDatabasePath(databasePath)
	if err != nil {
		return false
	}
	return normalized == sqliteProductDBPath
}

// mcpServersContainSQLiteNPXPackage 检查新增配置里是否包含 sqlite npx 包，用于决定是否需要解析产品 DB 路径。
func mcpServersContainSQLiteNPXPackage(input map[string]ServerConfig) bool {
	for _, config := range input {
		if stdioCommandBase(config.Command) != "npx" {
			continue
		}
		for _, arg := range config.Args {
			if isSQLiteNPXPackage(strings.TrimSpace(arg)) {
				return true
			}
		}
	}
	return false
}

func isSQLiteNPXPackage(arg string) bool {
	switch arg {
	case defaultSQLitePackage, legacyDefaultSQLitePackage, brokenSQLitePackage:
		return true
	default:
		return false
	}
}

func stdioCommandBase(command string) string {
	base := strings.ToLower(strings.TrimSpace(filepath.Base(command)))
	return strings.TrimSuffix(strings.TrimSuffix(base, ".exe"), ".cmd")
}

func normalizeHeaders(serverName string, input map[string]string) (map[string]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	if err := httpegress.ValidateHeaders(input); err != nil {
		return nil, fmt.Errorf("mcp_server: %w", err)
	}
	return normalizeStringMap(input, errMissingHeaderName, errMissingHeaderValue, serverName)
}

func normalizeStringList(input []string, emptyErr error, label string) ([]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(input))
	for _, rawValue := range input {
		value := strings.TrimSpace(rawValue)
		if value == "" {
			return nil, fmt.Errorf("%w: %s", emptyErr, label)
		}
		out = append(out, value)
	}
	return out, nil
}

func normalizeStringMap(input map[string]string, emptyKeyErr, emptyValueErr error, label string) (map[string]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(input))
	for rawName, rawValue := range input {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, fmt.Errorf("%w: %s", emptyKeyErr, label)
		}
		value := strings.TrimSpace(rawValue)
		if value == "" {
			return nil, fmt.Errorf("%w: %s.%s", emptyValueErr, label, name)
		}
		out[name] = value
	}
	return out, nil
}

func normalizeServerEnabled(enabled *bool) *bool {
	if enabled == nil {
		return boolPtr(true)
	}
	return boolPtr(*enabled)
}

func mcpServerConfigEnabled(config ServerConfig) bool {
	return config.Enabled == nil || *config.Enabled
}

func boolPtr(value bool) *bool {
	return &value
}

func validateHTTPURL(rawURL string) error {
	if _, err := httpegress.ValidatePublicURL(rawURL); err != nil {
		return fmt.Errorf("%w: %v", errInvalidServerURL, err)
	}
	return nil
}
