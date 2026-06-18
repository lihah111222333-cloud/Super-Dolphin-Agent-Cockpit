package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/httpegress"
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
	errMissingWorkspaceRoot        = errors.New("mcp_server: workspaceRoot is required")
	errMCPServerStoreNotConfigured = errors.New("mcp_server: config store is not configured")
	errMCPServerToolsRequestFailed = errors.New("mcp_server: tools request failed")
	errInvalidToolsResponse        = errors.New("mcp_server: invalid tools response")
	errPostgresInstallerMissing    = errors.New("mcp_server: postgres installer is not configured")
)

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
}

type ConfigDocument struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

type AddServersRequest = contract.MCPServerAddRequest

type DeleteServerRequest struct {
	ServerName string `json:"serverName"`
}

type ServerConfig = contract.MCPServerConfig

type AddServersResult = contract.MCPServerAddResult

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

type DeleteServerResult struct {
	ConfigPath string `json:"configPath"`
	ServerName string `json:"serverName"`
	Deleted    bool   `json:"deleted"`
}

type service struct {
	store             MCPServerConfigStore
	httpClient        mcpHTTPDoer
	postgresInstaller postgresInstaller
	sqlitePath        string
}

// NewService 创建服务。
func NewService() Service {
	return NewServiceWithStore(nil)
}

// NewServiceWithStore 创建带配置存储的 MCP server 服务。
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

func newServiceWithStoreAndInstaller(store MCPServerConfigStore, installer postgresInstaller) *service {
	return newServiceWithStoreInstallerAndSQLitePath(store, installer, "")
}

func newServiceWithStoreInstallerAndSQLitePath(store MCPServerConfigStore, installer postgresInstaller, sqlitePath string) *service {
	return &service{store: store, httpClient: defaultMCPHTTPClient, postgresInstaller: installer, sqlitePath: strings.TrimSpace(sqlitePath)}
}

// AddServers 添加servers。
func (s *service) AddServers(ctx context.Context, req AddServersRequest) (AddServersResult, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return AddServersResult{}, err
	}
	additions, names, err := normalizeMCPServers(req.MCPServers)
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

// ListServers 列出servers。
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

// ListServersForCWD 为工作目录列出servers。
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
	config, err = normalizeServerConfig(name, config)
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

// AsMCPServerConfigProvider 把mcp_server处理为MCP服务端配置provider。
func AsMCPServerConfigProvider(svc Service) contract.MCPServerConfigProvider {
	return mcpServerConfigProvider{svc: svc}
}

// ListMCPServerConfigs 列出MCP服务端配置。
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

func rejectExistingMCPServers(existing map[string]ServerConfig, names []string) error {
	for _, name := range names {
		if _, ok := existing[name]; ok {
			return fmt.Errorf("%w: %s", errServerAlreadyExists, name)
		}
	}
	return nil
}

func normalizeListServerToolsName(req ListServerToolsRequest) (string, error) {
	name := strings.TrimSpace(req.ServerName)
	if name == "" || name != req.ServerName {
		return "", errMissingServerName
	}
	return name, nil
}

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

func (s *service) mcpHTTPClient() mcpHTTPDoer {
	if s != nil && s.httpClient != nil {
		return s.httpClient
	}
	return defaultMCPHTTPClient
}

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
func normalizeMCPServers(input map[string]ServerConfig) (map[string]ServerConfig, []string, error) {
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
		config, err := normalizeServerConfig(name, rawConfig)
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
func normalizeServerConfig(name string, config ServerConfig) (ServerConfig, error) {
	transport := strings.TrimSpace(config.Transport)
	if transport == "" {
		return ServerConfig{}, fmt.Errorf("%w: %s", errMissingServerTransport, name)
	}
	switch strings.ToLower(transport) {
	case "http":
		return normalizeHTTPServerConfig(name, config)
	case "stdio":
		return normalizeStdioServerConfig(name, config)
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

func normalizeStdioServerConfig(name string, config ServerConfig) (ServerConfig, error) {
	command := strings.TrimSpace(config.Command)
	if command == "" {
		return ServerConfig{}, fmt.Errorf("%w: %s", errMissingServerCommand, name)
	}
	args, err := normalizeStringList(config.Args, errMissingServerArg, name)
	if err != nil {
		return ServerConfig{}, err
	}
	if !allowedStdioServerCommand(command, args) {
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

func allowedStdioServerCommand(command string, args []string) bool {
	base := strings.ToLower(strings.TrimSpace(filepath.Base(command)))
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".exe"), ".cmd")
	if base == defaultPostgresCommand {
		return true
	}
	if base != "npx" {
		return false
	}
	for _, arg := range args {
		switch strings.TrimSpace(arg) {
		case defaultPostgresPackage, defaultSQLitePackage, defaultPlaywrightPackage,
			legacyDefaultSQLitePackage, brokenSQLitePackage:
			return true
		}
	}
	return false
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
