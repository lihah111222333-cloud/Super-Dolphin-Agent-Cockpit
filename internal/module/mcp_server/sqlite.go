package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const (
	// DefaultSQLiteServerName 是按需开启的本地 SQLite MCP server 名称。
	DefaultSQLiteServerName = "sqlite"

	defaultSQLitePackage       = "@bytebase/dbhub"
	legacyDefaultSQLitePackage = "@modelcontextprotocol/server-sqlite"
	brokenSQLitePackage        = "mcp-server-sqlite"
)

// StartSQLiteServerRequest 是默认 sqlite MCP server 的显式启动请求。
type StartSQLiteServerRequest = contract.MCPSQLiteServerStartRequest

// StartSQLiteServerResult 返回 sqlite MCP server 配置的写入和开启状态。
type StartSQLiteServerResult = contract.MCPSQLiteServerStartResult

// StopSQLiteServerRequest 是默认 sqlite MCP server 的显式关闭请求。
type StopSQLiteServerRequest = contract.MCPSQLiteServerStopRequest

// StopSQLiteServerResult 返回 sqlite MCP server 关闭后的状态。
type StopSQLiteServerResult = contract.MCPSQLiteServerStopResult

// StartSQLiteServer 写入或重新启用默认 sqlite stdio MCP server 配置。
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

func (s *service) replaceDefaultSQLiteServer(ctx context.Context, config ServerConfig) (string, error) {
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
	deleted, err := store.DeleteServer(ctx, workspaceRoot, DefaultSQLiteServerName)
	if err != nil {
		return "", err
	}
	if !deleted {
		return "", fmt.Errorf("%w: %s", errServerNotFound, DefaultSQLiteServerName)
	}
	inserted, err := store.InsertServer(ctx, StoreMCPServerConfigParams{
		WorkspaceRoot: workspaceRoot,
		Name:          DefaultSQLiteServerName,
		Config:        config,
	})
	if err != nil {
		return "", err
	}
	if !inserted {
		return "", fmt.Errorf("%w: %s", errServerAlreadyExists, DefaultSQLiteServerName)
	}
	return mcpServerConfigPath(workspaceRoot), nil
}

func (s *service) setDefaultSQLiteServerEnabled(ctx context.Context, enabled bool) error {
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
	return nil
}

func (s *service) resolveSQLiteDatabasePath(requested string) (string, error) {
	for _, candidate := range []string{
		requested,
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
