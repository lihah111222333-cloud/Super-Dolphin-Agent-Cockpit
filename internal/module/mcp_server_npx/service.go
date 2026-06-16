package mcpservernpx

import (
	"context"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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
type StartPostgresServerRequest = contract.MCPPostgresServerStartRequest

// StartPostgresServerResult 返回配置写入位置以及本次是否新增配置。
type StartPostgresServerResult = contract.MCPPostgresServerStartResult

type service struct {
	servers contract.MCPPostgresServerStarter
}

// NewService 创建默认 npm MCP server 控制服务；它只响应显式 RPC 调用，不自动注入配置。
func NewService(servers contract.MCPPostgresServerStarter) Service {
	return &service{servers: servers}
}

// StartPostgresServer 通过现有 MCP server 配置服务写入默认 postgres server。
// 实际安装和写入逻辑委托给 mcp_server 模块，避免这个兼容入口和主入口产生不同默认配置。
func (s *service) StartPostgresServer(ctx context.Context, req StartPostgresServerRequest) (StartPostgresServerResult, error) {
	if s == nil || s.servers == nil {
		return StartPostgresServerResult{}, fmt.Errorf("mcp_server_npx: mcp server service is not configured")
	}
	result, err := s.servers.StartPostgresServer(ctx, req)
	if err != nil {
		return StartPostgresServerResult{}, err
	}
	return result, nil
}
