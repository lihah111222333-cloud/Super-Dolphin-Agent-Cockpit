package mcpservernpx

import (
	"context"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const (
	// DefaultPostgresServerName 是显式启动入口写入的本地 PostgreSQL MCP server 名称。
	DefaultPostgresServerName = "postgres"

	defaultPostgresDatabaseURL = "postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable"
)

// Service 暴露默认 npx MCP server 的显式控制入口。
type Service interface {
	StartPostgresServer(context.Context, StartPostgresServerRequest) (StartPostgresServerResult, error)
}

// StartPostgresServerRequest 是默认 postgres MCP server 的显式启动请求。
type StartPostgresServerRequest struct{}

// StartPostgresServerResult 返回配置写入位置以及本次是否新增配置。
type StartPostgresServerResult struct {
	ConfigPath string                   `json:"configPath"`
	ServerName string                   `json:"serverName"`
	Added      bool                     `json:"added"`
	Config     contract.MCPServerConfig `json:"config"`
}

type service struct {
	servers contract.MCPServerConfigWriter
}

// NewService 创建默认 npx MCP server 控制服务；它只响应显式 RPC 调用，不自动注入配置。
func NewService(servers contract.MCPServerConfigWriter) Service {
	return &service{servers: servers}
}

// DefaultPostgresServerConfig 返回默认 npx postgres MCP server 的 stdio 启动配置。
func DefaultPostgresServerConfig() contract.MCPServerConfig {
	return contract.MCPServerConfig{
		Transport: "stdio",
		Command:   "npx",
		Args: []string{
			"-y",
			"@modelcontextprotocol/server-postgres",
			defaultPostgresDatabaseURL,
		},
	}
}

// StartPostgresServer 通过现有 MCP server 配置服务写入默认 postgres server。
// 已存在时只返回当前配置，避免重复写入；真正的 stdio 进程仍由后续 provider 会话按配置拉起。
func (s *service) StartPostgresServer(ctx context.Context, _ StartPostgresServerRequest) (StartPostgresServerResult, error) {
	if s == nil || s.servers == nil {
		return StartPostgresServerResult{}, fmt.Errorf("mcp_server_npx: mcp server service is not configured")
	}
	listed, err := s.servers.ListServers(ctx)
	if err != nil {
		return StartPostgresServerResult{}, err
	}
	config := DefaultPostgresServerConfig()
	if existing, ok := listed.MCPServers[DefaultPostgresServerName]; ok {
		return StartPostgresServerResult{
			ConfigPath: listed.ConfigPath,
			ServerName: DefaultPostgresServerName,
			Added:      false,
			Config:     existing,
		}, nil
	}
	added, err := s.servers.AddServers(ctx, contract.MCPServerAddRequest{
		MCPServers: map[string]contract.MCPServerConfig{
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
