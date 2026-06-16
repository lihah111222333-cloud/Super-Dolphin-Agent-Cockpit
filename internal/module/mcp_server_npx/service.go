package mcpservernpx

import (
	"context"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpserver "github.com/anthropic-ai/super-agent-v3/internal/module/mcp_server"
)

const (
	// DefaultPostgresServerName 是显式启动入口写入的本地 PostgreSQL MCP server 名称。
	DefaultPostgresServerName = "postgres"
)

// Service 暴露默认 npm MCP server 的显式控制入口。
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
	servers mcpserver.Service
}

// NewService 创建默认 npm MCP server 控制服务；它只响应显式 RPC 调用，不自动注入配置。
func NewService(servers mcpserver.Service) Service {
	return &service{servers: servers}
}

// DefaultPostgresServerConfig 返回默认 postgres MCP server 的 stdio 启动配置。
func DefaultPostgresServerConfig() contract.MCPServerConfig {
	return mcpserver.DefaultPostgresServerConfig()
}

// StartPostgresServer 通过现有 MCP server 配置服务写入默认 postgres server。
// 实际安装和写入逻辑委托给 mcp_server 模块，避免这个兼容入口和主入口产生不同默认配置。
func (s *service) StartPostgresServer(ctx context.Context, _ StartPostgresServerRequest) (StartPostgresServerResult, error) {
	if s == nil || s.servers == nil {
		return StartPostgresServerResult{}, fmt.Errorf("mcp_server_npx: mcp server service is not configured")
	}
	result, err := s.servers.StartPostgresServer(ctx, mcpserver.StartPostgresServerRequest{})
	if err != nil {
		return StartPostgresServerResult{}, err
	}
	return StartPostgresServerResult{
		ConfigPath: result.ConfigPath,
		ServerName: result.ServerName,
		Added:      result.Added,
		Config:     result.Config,
	}, nil
}
