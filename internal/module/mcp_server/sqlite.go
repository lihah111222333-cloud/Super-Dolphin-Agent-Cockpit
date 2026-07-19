package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

const (
	// DefaultSQLiteServerName 是按需开启的本地 SQLite MCP server 名称。
	DefaultSQLiteServerName = "sqlite"

	defaultSQLitePackage       = "@bytebase/dbhub"
	legacyDefaultSQLitePackage = "@modelcontextprotocol/server-sqlite"
	brokenSQLitePackage        = "mcp-server-sqlite"
)

var errSQLiteRequestDatabasePathUnsupported = errors.New("mcp_server: sqlite databasePath request override is not allowed")

// StartSQLiteServerRequest 是默认 sqlite MCP server 的显式启动 RPC 请求。
// DatabasePath 保留 wire 兼容但不允许请求覆盖；数据库路径只能来自运行时配置或受信环境。
type StartSQLiteServerRequest = contract.MCPSQLiteServerStartRequest

// StartSQLiteServerResult 返回 sqlite 配置写入位置、本次是否新增以及最终 enabled 状态。
type StartSQLiteServerResult = contract.MCPSQLiteServerStartResult

// StopSQLiteServerRequest 是默认 sqlite MCP server 的显式关闭 RPC 请求。
type StopSQLiteServerRequest = contract.MCPSQLiteServerStopRequest

// StopSQLiteServerResult 返回 sqlite 配置路径和关闭后的 enabled 状态。
type StopSQLiteServerResult = contract.MCPSQLiteServerStopResult

// StartSQLiteServer 写入或重新启用默认 sqlite stdio MCP server 配置。
// 已存在可自动识别的内置默认配置时会转换到当前 dbhub 形态；用户自定义同名配置只切换 enabled。
func (s *service) StartSQLiteServer(ctx context.Context, req StartSQLiteServerRequest) (StartSQLiteServerResult, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return StartSQLiteServerResult{}, err
	}
	if s == nil {
		return StartSQLiteServerResult{}, errMCPServerStoreNotConfigured
	}
	databasePath, err := s.resolveSQLiteDatabasePath(req.DatabasePath)
	if err != nil {
		return StartSQLiteServerResult{}, err
	}
	config := defaultSQLiteServerConfig(databasePath)
	listed, err := s.ListServers(ctx)
	if err != nil {
		return StartSQLiteServerResult{}, err
	}
	if existing, ok := listed.MCPServers[DefaultSQLiteServerName]; ok {
		if isLegacyDefaultSQLiteServerConfig(existing) {
			configPath, err := s.replaceDefaultSQLiteServer(ctx, config)
			if err != nil {
				return StartSQLiteServerResult{}, err
			}
			return StartSQLiteServerResult{
				ConfigPath: configPath,
				ServerName: DefaultSQLiteServerName,
				Added:      false,
				Enabled:    true,
				Config:     config,
			}, nil
		}
		if err := s.setDefaultSQLiteServerEnabled(ctx, true); err != nil {
			return StartSQLiteServerResult{}, err
		}
		existing.Enabled = boolPtr(true)
		return StartSQLiteServerResult{
			ConfigPath: listed.ConfigPath,
			ServerName: DefaultSQLiteServerName,
			Added:      false,
			Enabled:    true,
			Config:     existing,
		}, nil
	}
	added, err := s.AddServers(ctx, AddServersRequest{
		MCPServers: map[string]ServerConfig{
			DefaultSQLiteServerName: config,
		},
	})
	if err != nil {
		return StartSQLiteServerResult{}, err
	}
	return StartSQLiteServerResult{
		ConfigPath: added.ConfigPath,
		ServerName: DefaultSQLiteServerName,
		Added:      true,
		Enabled:    true,
		Config:     config,
	}, nil
}

// StopSQLiteServer 关闭默认 sqlite MCP server，但保留配置行供后续 start 复用。
func (s *service) StopSQLiteServer(ctx context.Context, _ StopSQLiteServerRequest) (StopSQLiteServerResult, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return StopSQLiteServerResult{}, err
	}
	if s == nil {
		return StopSQLiteServerResult{}, errMCPServerStoreNotConfigured
	}
	if err := s.setDefaultSQLiteServerEnabled(ctx, false); err != nil {
		return StopSQLiteServerResult{}, err
	}
	workspaceRoot, _, err := s.resolveWorkspaceServers(ctx, "")
	if err != nil {
		return StopSQLiteServerResult{}, err
	}
	return StopSQLiteServerResult{
		ConfigPath: mcpServerConfigPath(workspaceRoot),
		ServerName: DefaultSQLiteServerName,
		Enabled:    false,
	}, nil
}

func defaultSQLiteServerConfig(databasePath string) ServerConfig {
	return ServerConfig{
		Transport: "stdio",
		Command:   "npx",
		Args: []string{
			"-y",
			defaultSQLitePackage,
			"--dsn=" + sqliteDBHubDSN(databasePath),
		},
		Enabled: boolPtr(true),
	}
}

func isLegacyDefaultSQLiteServerConfig(config ServerConfig) bool {
	if !strings.EqualFold(strings.TrimSpace(config.Transport), "stdio") ||
		strings.TrimSpace(config.Command) != "npx" ||
		len(config.Env) != 0 {
		return false
	}
	return legacySQLiteDatabasePath(config.Args) != ""
}

// legacySQLiteDatabasePath 识别可自动转换的 sqlite server 参数并提取数据库路径。
// 只匹配项目内置默认形态，避免误把用户自定义 server 当作可转换配置。
func legacySQLiteDatabasePath(args []string) string {
	if len(args) == 3 &&
		strings.TrimSpace(args[0]) == "-y" &&
		strings.TrimSpace(args[1]) == legacyDefaultSQLitePackage {
		return strings.TrimSpace(args[2])
	}
	if len(args) == 4 &&
		strings.TrimSpace(args[0]) == "-y" &&
		strings.TrimSpace(args[1]) == brokenSQLitePackage &&
		isSQLiteDBPathFlag(args[2]) {
		return strings.TrimSpace(args[3])
	}
	return ""
}

func isSQLiteDBPathFlag(flag string) bool {
	switch strings.TrimSpace(flag) {
	case "--db", "--database":
		return true
	default:
		return false
	}
}

func sqliteDBHubDSN(databasePath string) string {
	path := strings.TrimSpace(databasePath)
	if path == "" {
		return ""
	}
	return "sqlite:///" + filepath.ToSlash(path)
}

// replaceDefaultSQLiteServer 用当前 dbhub 配置替换可自动转换的默认 sqlite server。
// store 必须原子更新已存在的 exact workspace/name，成功后才推进配置 revision。
func (s *service) replaceDefaultSQLiteServer(ctx context.Context, config ServerConfig) (string, error) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	workspaceRoot, servers, err := s.resolveWorkspaceServers(ctx, "")
	if err != nil {
		return "", err
	}
	if _, ok := servers[DefaultSQLiteServerName]; !ok {
		return "", fmt.Errorf("%w: %s", errServerNotFound, DefaultSQLiteServerName)
	}
	store, err := s.requireStore()
	if err != nil {
		return "", err
	}
	replaced, err := store.ReplaceServer(ctx, StoreMCPServerConfigParams{
		WorkspaceRoot: workspaceRoot,
		Name:          DefaultSQLiteServerName,
		Config:        config,
	})
	if err != nil {
		return "", err
	}
	if !replaced {
		return "", fmt.Errorf("%w: %s", errServerNotFound, DefaultSQLiteServerName)
	}
	s.configRevision++
	return mcpServerConfigPath(workspaceRoot), nil
}

// setDefaultSQLiteServerEnabled 只切换默认 sqlite server 的 enabled 状态。
// 配置行和 store 都必须存在，否则返回错误，避免 start/stop 静默失效。
func (s *service) setDefaultSQLiteServerEnabled(ctx context.Context, enabled bool) error {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	workspaceRoot, servers, err := s.resolveWorkspaceServers(ctx, "")
	if err != nil {
		return err
	}
	if _, ok := servers[DefaultSQLiteServerName]; !ok {
		return fmt.Errorf("%w: %s", errServerNotFound, DefaultSQLiteServerName)
	}
	store, err := s.requireStore()
	if err != nil {
		return err
	}
	updated, err := store.SetServerEnabled(ctx, workspaceRoot, DefaultSQLiteServerName, enabled)
	if err != nil {
		return err
	}
	if !updated {
		return fmt.Errorf("%w: %s", errServerNotFound, DefaultSQLiteServerName)
	}
	s.configRevision++
	return nil
}

// resolveSQLiteDatabasePath 按运行时配置和环境变量解析 SQLite 数据库路径。
// 请求体不允许覆盖数据库位置，避免前端或远程调用把 sqlite MCP 指向任意本地文件。
func (s *service) resolveSQLiteDatabasePath(requested string) (string, error) {
	if strings.TrimSpace(requested) != "" {
		return "", errSQLiteRequestDatabasePathUnsupported
	}
	for _, candidate := range []string{
		s.sqlitePath,
		os.Getenv(contract.SQLitePathEnvKey),
		os.Getenv(contract.InternalSQLitePathEnvKey),
	} {
		if path, err := normalizeSQLiteDatabasePath(candidate); err == nil && path != "" {
			return path, nil
		} else if err != nil {
			return "", err
		}
	}
	if home := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_HOME")); home != "" {
		return normalizeSQLiteDatabasePath(filepath.Join(home, "super-dolphin.db"))
	}
	return "", errors.New("mcp_server: sqlite database path is required")
}

func normalizeSQLiteDatabasePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("mcp_server: resolve sqlite database path: %w", err)
	}
	return filepath.Clean(abs), nil
}
