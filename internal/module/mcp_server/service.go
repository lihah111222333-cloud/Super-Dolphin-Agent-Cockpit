package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

var (
	errMissingMCPServers           = errors.New("mcp_server: mcpServers is required")
	errMissingServerName           = errors.New("mcp_server: server name is required")
	errDuplicateServerName         = errors.New("mcp_server: duplicate server name")
	errMissingServerTransport      = errors.New("mcp_server: transport is required")
	errUnsupportedTransport        = errors.New("mcp_server: unsupported transport")
	errMissingServerURL            = errors.New("mcp_server: url is required")
	errInvalidServerURL            = errors.New("mcp_server: invalid url")
	errMissingHeaderName           = errors.New("mcp_server: header name is required")
	errMissingHeaderValue          = errors.New("mcp_server: header value is required")
	errInvalidConfigDocument       = errors.New("mcp_server: invalid config document")
	errServerAlreadyExists         = errors.New("mcp_server: server already exists")
	errServerNotFound              = errors.New("mcp_server: server not found")
	errMissingWorkspaceRoot        = errors.New("mcp_server: workspaceRoot is required")
	errMCPServerStoreNotConfigured = errors.New("mcp_server: config store is not configured")
	errMCPServerToolsRequestFailed = errors.New("mcp_server: tools request failed")
	errInvalidToolsResponse        = errors.New("mcp_server: invalid tools response")
)

type Service interface {
	AddServers(context.Context, AddServersRequest) (AddServersResult, error)
	ListServers(context.Context) (ListServersResult, error)
	ListServersForCWD(context.Context, string) (ListServersResult, error)
	ListServerTools(context.Context, ListServerToolsRequest) (ListServerToolsResult, error)
	DeleteServer(context.Context, DeleteServerRequest) (DeleteServerResult, error)
}

type ConfigDocument struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

type AddServersRequest struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

type DeleteServerRequest struct {
	ServerName string `json:"serverName"`
}

type ServerConfig = contract.MCPServerConfig

type AddServersResult struct {
	ConfigPath  string   `json:"configPath"`
	ServerNames []string `json:"serverNames"`
}

type ListServersResult struct {
	ConfigPath string                  `json:"configPath"`
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

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
	store      MCPServerConfigStore
	httpClient mcpHTTPDoer
}

// NewService 创建服务。
func NewService() Service {
	return NewServiceWithStore(nil)
}

// NewServiceWithStore 创建带配置存储的 MCP server 服务。
func NewServiceWithStore(store MCPServerConfigStore) Service {
	return &service{store: store, httpClient: defaultMCPHTTPClient}
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
	return mcpServersToContract(result.MCPServers), nil
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

// normalizeServerConfig 规范化服务端配置。
func normalizeServerConfig(name string, config ServerConfig) (ServerConfig, error) {
	transport := strings.TrimSpace(config.Transport)
	if transport == "" {
		return ServerConfig{}, fmt.Errorf("%w: %s", errMissingServerTransport, name)
	}
	if transport != "http" {
		return ServerConfig{}, fmt.Errorf("%w: %s", errUnsupportedTransport, transport)
	}
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
		Transport: transport,
		URL:       rawURL,
		Headers:   headers,
	}, nil
}

func normalizeHeaders(serverName string, input map[string]string) (map[string]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	headers := make(map[string]string, len(input))
	for rawName, rawValue := range input {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, fmt.Errorf("%w: %s", errMissingHeaderName, serverName)
		}
		value := strings.TrimSpace(rawValue)
		if value == "" {
			return nil, fmt.Errorf("%w: %s.%s", errMissingHeaderValue, serverName, name)
		}
		headers[name] = value
	}
	return headers, nil
}

func validateHTTPURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return errInvalidServerURL
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errInvalidServerURL
	}
	if parsed.Host == "" {
		return errInvalidServerURL
	}
	return nil
}

// readMCPServerConfig 读取MCP服务端配置。
func readMCPServerConfig(configPath string) (ConfigDocument, error) {
	raw, err := os.ReadFile(configPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return ConfigDocument{MCPServers: map[string]ServerConfig{}}, nil
	case err != nil:
		return ConfigDocument{}, fmt.Errorf("read mcp server config: %w", err)
	}
	var doc ConfigDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ConfigDocument{}, fmt.Errorf("%w: %v", errInvalidConfigDocument, err)
	}
	if doc.MCPServers == nil {
		return ConfigDocument{}, errInvalidConfigDocument
	}
	for name, config := range doc.MCPServers {
		normalized, err := normalizeServerConfig(name, config)
		if err != nil {
			return ConfigDocument{}, fmt.Errorf("%w: %v", errInvalidConfigDocument, err)
		}
		doc.MCPServers[name] = normalized
	}
	return doc, nil
}

func writeMCPServerConfig(configPath string, doc ConfigDocument) error {
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal mcp server config: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("create mcp server config dir: %w", err)
	}
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		return fmt.Errorf("write mcp server config: %w", err)
	}
	return nil
}

func cloneMCPServers(input map[string]ServerConfig) map[string]ServerConfig {
	out := make(map[string]ServerConfig, len(input))
	for name, config := range input {
		headers := make(map[string]string, len(config.Headers))
		for header, value := range config.Headers {
			headers[header] = value
		}
		if len(headers) == 0 {
			headers = nil
		}
		config.Headers = headers
		out[name] = config
	}
	return out
}

// mcpServersToContract 把MCPservers处理为contract。
func mcpServersToContract(input map[string]ServerConfig) map[string]contract.MCPServerConfig {
	out := make(map[string]contract.MCPServerConfig, len(input))
	for name, config := range input {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		headers := make(map[string]string, len(config.Headers))
		for header, value := range config.Headers {
			headers[header] = value
		}
		if len(headers) == 0 {
			headers = nil
		}
		out[name] = contract.MCPServerConfig{
			Transport: strings.TrimSpace(config.Transport),
			URL:       strings.TrimSpace(config.URL),
			Headers:   headers,
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolveMCPServerConfigBaseDir 解析MCP服务端配置base目录。
func resolveMCPServerConfigBaseDir(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return os.Getwd()
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	for dir := filepath.Clean(cwd); dir != ""; dir = filepath.Dir(dir) {
		configPath := mcpServerConfigPath(dir)
		if _, err := os.Stat(configPath); err == nil {
			return dir, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat mcp server config: %w", err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return filepath.Clean(cwd), nil
}

func mcpServerConfigPath(workingDir string) string {
	return filepath.Join(workingDir, ".agent", "mcp_server", "config.json")
}

func currentMCPServerWorkspaceRoot() (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return filepath.Clean(workingDir), nil
}

func normalizeMCPServerWorkspaceRoot(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return currentMCPServerWorkspaceRoot()
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	return filepath.Clean(cwd), nil
}

func mcpServerWorkspaceCandidates(workspaceRoot string) []string {
	workspaceRoot = filepath.Clean(workspaceRoot)
	candidates := make([]string, 0, 4)
	for dir := workspaceRoot; dir != ""; dir = filepath.Dir(dir) {
		candidates = append(candidates, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return candidates
}
