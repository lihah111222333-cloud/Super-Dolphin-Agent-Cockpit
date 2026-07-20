package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

const (
	// DefaultSQLiteServerName 是按需开启的本地 SQLite MCP server 名称。
	DefaultSQLiteServerName = "sqlite"

	defaultSQLitePackage       = "@bytebase/dbhub@0.23.0"
	unpinnedSQLitePackage      = "@bytebase/dbhub"
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
	s.configMu.Lock()
	defer s.configMu.Unlock()
	return s.startSQLiteServerLocked(ctx, databasePath)
}

// startSQLiteServerLocked 在同一配置临界区内重读、判定并写入默认 sqlite server。
func (s *service) startSQLiteServerLocked(ctx context.Context, databasePath string) (StartSQLiteServerResult, error) {
	workspaceRoot, servers, err := s.resolveWorkspaceServers(ctx, "")
	if err != nil {
		return StartSQLiteServerResult{}, err
	}
	store, err := s.requireStore()
	if err != nil {
		return StartSQLiteServerResult{}, err
	}
	configPath := mcpServerConfigPath(workspaceRoot)
	config := defaultSQLiteServerConfig(databasePath)
	if existing, ok := servers[DefaultSQLiteServerName]; ok {
		return s.startExistingSQLiteServerLocked(ctx, store, workspaceRoot, configPath, databasePath, existing, config)
	}
	return s.insertSQLiteServerLocked(ctx, store, workspaceRoot, configPath, config)
}

// startExistingSQLiteServerLocked 基于锁内快照替换内置配置或启用当前自定义配置。
func (s *service) startExistingSQLiteServerLocked(
	ctx context.Context,
	store MCPServerConfigStore,
	workspaceRoot string,
	configPath string,
	databasePath string,
	existing ServerConfig,
	config ServerConfig,
) (StartSQLiteServerResult, error) {
	if isUpgradeableDefaultSQLiteServerConfig(existing, databasePath) {
		replaced, err := store.ReplaceServer(ctx, StoreMCPServerConfigParams{
			WorkspaceRoot: workspaceRoot,
			Name:          DefaultSQLiteServerName,
			Config:        config,
		})
		if err != nil {
			return StartSQLiteServerResult{}, err
		}
		if !replaced {
			return StartSQLiteServerResult{}, fmt.Errorf("%w: %s", errServerNotFound, DefaultSQLiteServerName)
		}
		s.configRevision++
		return startedSQLiteServerResult(configPath, config, false), nil
	}
	updated, err := store.SetServerEnabled(ctx, workspaceRoot, DefaultSQLiteServerName, true)
	if err != nil {
		return StartSQLiteServerResult{}, err
	}
	if !updated {
		return StartSQLiteServerResult{}, fmt.Errorf("%w: %s", errServerNotFound, DefaultSQLiteServerName)
	}
	s.configRevision++
	existing.Enabled = boolPtr(true)
	return startedSQLiteServerResult(configPath, existing, false), nil
}

// insertSQLiteServerLocked 在锁内确认缺失后插入默认 sqlite server。
func (s *service) insertSQLiteServerLocked(
	ctx context.Context,
	store MCPServerConfigStore,
	workspaceRoot string,
	configPath string,
	config ServerConfig,
) (StartSQLiteServerResult, error) {
	inserted, err := store.InsertServer(ctx, StoreMCPServerConfigParams{
		WorkspaceRoot: workspaceRoot,
		Name:          DefaultSQLiteServerName,
		Config:        config,
	})
	if err != nil {
		return StartSQLiteServerResult{}, err
	}
	if !inserted {
		return StartSQLiteServerResult{}, fmt.Errorf("%w: %s", errServerAlreadyExists, DefaultSQLiteServerName)
	}
	s.configRevision++
	return startedSQLiteServerResult(configPath, config, true), nil
}

func startedSQLiteServerResult(configPath string, config ServerConfig, added bool) StartSQLiteServerResult {
	return StartSQLiteServerResult{
		ConfigPath: configPath,
		ServerName: DefaultSQLiteServerName,
		Added:      added,
		Enabled:    true,
		Config:     config,
	}
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

// isUpgradeableDefaultSQLiteServerConfig 只识别可安全替换的历史默认或精确未固定默认配置。
func isUpgradeableDefaultSQLiteServerConfig(config ServerConfig, databasePath string) bool {
	return isLegacyDefaultSQLiteServerConfig(config) || isUnpinnedDefaultSQLiteServerConfig(config, databasePath)
}

func isUnpinnedDefaultSQLiteServerConfig(config ServerConfig, databasePath string) bool {
	return config.Transport == "stdio" &&
		config.Command == "npx" &&
		len(config.Env) == 0 &&
		slices.Equal(config.Args, []string{"-y", unpinnedSQLitePackage, "--dsn=" + sqliteDBHubDSN(databasePath)})
}

// isUnpinnedSQLiteServerConfigCandidate 识别 provider 路径需要进一步绑定受信数据库路径的未固定候选。
func isUnpinnedSQLiteServerConfigCandidate(config ServerConfig) bool {
	return config.Transport == "stdio" &&
		config.Command == "npx" &&
		len(config.Env) == 0 &&
		len(config.Args) == 3 &&
		config.Args[0] == "-y" &&
		config.Args[1] == unpinnedSQLitePackage
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

type sqliteConfigMigrationResult struct {
	Config ServerConfig
	Exists bool
}

// migrateUnpinnedSQLiteServerConfig 在锁内重读并原子替换精确未固定默认配置。
func (s *service) migrateUnpinnedSQLiteServerConfig(
	ctx context.Context,
	cwd string,
	databasePath string,
) (sqliteConfigMigrationResult, error) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	workspaceRoot, servers, err := s.resolveWorkspaceServers(ctx, cwd)
	if err != nil {
		return sqliteConfigMigrationResult{}, err
	}
	existing, ok := servers[DefaultSQLiteServerName]
	if !ok {
		return sqliteConfigMigrationResult{}, nil
	}
	if !isUnpinnedDefaultSQLiteServerConfig(existing, databasePath) {
		return sqliteConfigMigrationResult{Config: existing, Exists: true}, nil
	}
	config := defaultSQLiteServerConfig(databasePath)
	config.Enabled = cloneBoolPtr(existing.Enabled)
	store, err := s.requireStore()
	if err != nil {
		return sqliteConfigMigrationResult{}, err
	}
	replaced, err := store.ReplaceServer(ctx, StoreMCPServerConfigParams{
		WorkspaceRoot: workspaceRoot,
		Name:          DefaultSQLiteServerName,
		Config:        config,
	})
	if err != nil {
		return sqliteConfigMigrationResult{}, err
	}
	if !replaced {
		return sqliteConfigMigrationResult{}, fmt.Errorf("%w: %s", errServerNotFound, DefaultSQLiteServerName)
	}
	s.configRevision++
	return sqliteConfigMigrationResult{Config: config, Exists: true}, nil
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
