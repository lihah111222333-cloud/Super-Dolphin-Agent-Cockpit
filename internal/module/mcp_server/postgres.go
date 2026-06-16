package mcpserver

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

const (
	// DefaultPostgresServerName 是显式启动入口写入的本地 PostgreSQL MCP server 名称。
	DefaultPostgresServerName = "postgres"

	defaultPostgresDatabaseURL = "postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable"
	defaultPostgresPackage     = "@modelcontextprotocol/server-postgres"
	defaultPostgresCommand     = "mcp-server-postgres"
)

// StartPostgresServerRequest 是默认 postgres MCP server 的显式启动请求。
type StartPostgresServerRequest struct{}

// StartPostgresServerResult 返回配置写入位置以及本次是否新增配置。
type StartPostgresServerResult struct {
	ConfigPath string       `json:"configPath"`
	ServerName string       `json:"serverName"`
	Added      bool         `json:"added"`
	Config     ServerConfig `json:"config"`
}

// StartPostgresServer 写入默认 postgres stdio MCP server 配置。
// 这个入口只在 RPC 被显式调用时生效；首次写入前会确保 npm 全局包已安装，后续 provider 直接拉起本地命令。
func (s *service) StartPostgresServer(ctx context.Context, _ StartPostgresServerRequest) (StartPostgresServerResult, error) {
	ctx = mcpServerContext(ctx)
	if err := ctx.Err(); err != nil {
		return StartPostgresServerResult{}, err
	}
	listed, err := s.ListServers(ctx)
	if err != nil {
		return StartPostgresServerResult{}, err
	}
	config := defaultPostgresServerConfig()
	if existing, ok := listed.MCPServers[DefaultPostgresServerName]; ok {
		if isLegacyDefaultPostgresServerConfig(existing) {
			if err := s.ensureDefaultPostgresInstalled(ctx); err != nil {
				return StartPostgresServerResult{}, err
			}
			configPath, err := s.replaceDefaultPostgresServer(ctx, config)
			if err != nil {
				return StartPostgresServerResult{}, err
			}
			return StartPostgresServerResult{
				ConfigPath: configPath,
				ServerName: DefaultPostgresServerName,
				Added:      false,
				Config:     config,
			}, nil
		}
		return StartPostgresServerResult{
			ConfigPath: listed.ConfigPath,
			ServerName: DefaultPostgresServerName,
			Added:      false,
			Config:     existing,
		}, nil
	}
	if err := s.ensureDefaultPostgresInstalled(ctx); err != nil {
		return StartPostgresServerResult{}, err
	}
	added, err := s.AddServers(ctx, AddServersRequest{
		MCPServers: map[string]ServerConfig{
			DefaultPostgresServerName: config,
		},
	})
	if err != nil {
		return StartPostgresServerResult{}, err
	}
	return StartPostgresServerResult{
		ConfigPath: added.ConfigPath,
		ServerName: DefaultPostgresServerName,
		Added:      true,
		Config:     config,
	}, nil
}

// DefaultPostgresServerConfig 返回默认 postgres MCP server 的 stdio 启动配置。
// 配置使用 npm 全局安装后的本地命令，避免 provider 每次会话通过 npx 拉取包。
func DefaultPostgresServerConfig() ServerConfig {
	return defaultPostgresServerConfig()
}

func defaultPostgresServerConfig() ServerConfig {
	return ServerConfig{
		Transport: "stdio",
		Command:   defaultPostgresCommand,
		Args: []string{
			defaultPostgresDatabaseURL,
		},
	}
}

func isLegacyDefaultPostgresServerConfig(config ServerConfig) bool {
	return strings.EqualFold(strings.TrimSpace(config.Transport), "stdio") &&
		strings.TrimSpace(config.Command) == "npx" &&
		slices.Equal(config.Args, []string{"-y", defaultPostgresPackage, defaultPostgresDatabaseURL}) &&
		len(config.Env) == 0
}

// replaceDefaultPostgresServer 只迁移完全匹配旧默认值的 postgres 配置。
// 这里不碰自定义 command，避免把用户手写的 MCP server 误改成内置默认值。
func (s *service) replaceDefaultPostgresServer(ctx context.Context, config ServerConfig) (string, error) {
	workspaceRoot, servers, err := s.resolveWorkspaceServers(ctx, "")
	if err != nil {
		return "", err
	}
	if _, ok := servers[DefaultPostgresServerName]; !ok {
		return "", fmt.Errorf("%w: %s", errServerNotFound, DefaultPostgresServerName)
	}
	store, err := s.requireStore()
	if err != nil {
		return "", err
	}
	deleted, err := store.DeleteServer(ctx, workspaceRoot, DefaultPostgresServerName)
	if err != nil {
		return "", err
	}
	if !deleted {
		return "", fmt.Errorf("%w: %s", errServerNotFound, DefaultPostgresServerName)
	}
	inserted, err := store.InsertServer(ctx, StoreMCPServerConfigParams{
		WorkspaceRoot: workspaceRoot,
		Name:          DefaultPostgresServerName,
		Config:        config,
	})
	if err != nil {
		return "", err
	}
	if !inserted {
		return "", fmt.Errorf("%w: %s", errServerAlreadyExists, DefaultPostgresServerName)
	}
	return mcpServerConfigPath(workspaceRoot), nil
}

// ensureDefaultPostgresInstalled 在写入默认配置前准备本地命令。
// 安装器缺失属于装配错误，直接返回错误，避免写入一个后续无法启动的 stdio 配置。
func (s *service) ensureDefaultPostgresInstalled(ctx context.Context) error {
	if s == nil || s.postgresInstaller == nil {
		return errPostgresInstallerMissing
	}
	return s.postgresInstaller.EnsureInstalled(ctx)
}
