package mcpservernpx

import (
	"context"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const (
	// DefaultPostgresServerName 是显式启动入口写入的本地 PostgreSQL MCP server 名称。
	DefaultPostgresServerName = "postgres"

	// DefaultSQLiteServerName 是显式启动入口写入的本地 SQLite MCP server 名称。
	DefaultSQLiteServerName = "sqlite"

	// DefaultPlaywrightServerName 是显式启动入口写入的本地 Playwright MCP server 名称。
	DefaultPlaywrightServerName = "playwright"
)

// Service 暴露默认 npm MCP server 的显式控制入口。
type Service interface {
	StartPostgresServer(context.Context, StartPostgresServerRequest) (StartPostgresServerResult, error)
	StartSQLiteServer(context.Context, StartSQLiteServerRequest) (StartSQLiteServerResult, error)
	StopSQLiteServer(context.Context, StopSQLiteServerRequest) (StopSQLiteServerResult, error)
	StartPlaywrightServer(context.Context, StartPlaywrightServerRequest) (StartPlaywrightServerResult, error)
	StopPlaywrightServer(context.Context, StopPlaywrightServerRequest) (StopPlaywrightServerResult, error)
}

// StartPostgresServerRequest 是兼容入口透传到底层 mcp_server 的 postgres 启动请求别名。
type StartPostgresServerRequest = contract.MCPPostgresServerStartRequest

// StartPostgresServerResult 是 postgres 默认配置写入结果的 wire 别名。
// Added 字段由底层 mcp_server 决定，兼容入口不重新解释结果。
type StartPostgresServerResult = contract.MCPPostgresServerStartResult

// StartSQLiteServerRequest 是兼容入口透传的 sqlite 启动请求别名。
type StartSQLiteServerRequest = contract.MCPSQLiteServerStartRequest

// StartSQLiteServerResult 是 sqlite 默认 server 写入或启用后的 wire 结果别名。
type StartSQLiteServerResult = contract.MCPSQLiteServerStartResult

// StopSQLiteServerRequest 是兼容入口透传的 sqlite 关闭请求别名。
type StopSQLiteServerRequest = contract.MCPSQLiteServerStopRequest

// StopSQLiteServerResult 是 sqlite 默认 server 关闭后的 wire 结果别名。
type StopSQLiteServerResult = contract.MCPSQLiteServerStopResult

// StartPlaywrightServerRequest 是兼容入口透传的 playwright 启动请求别名。
type StartPlaywrightServerRequest = contract.MCPPlaywrightServerStartRequest

// StartPlaywrightServerResult 是 playwright 默认 server 写入或启用后的 wire 结果别名。
type StartPlaywrightServerResult = contract.MCPPlaywrightServerStartResult

// StopPlaywrightServerRequest 是兼容入口透传的 playwright 关闭请求别名。
type StopPlaywrightServerRequest = contract.MCPPlaywrightServerStopRequest

// StopPlaywrightServerResult 是 playwright 默认 server 关闭后的 wire 结果别名。
type StopPlaywrightServerResult = contract.MCPPlaywrightServerStopResult

type defaultServerController interface {
	contract.MCPPostgresServerStarter
	contract.MCPSQLiteServerController
	contract.MCPPlaywrightServerController
}

type service struct {
	servers defaultServerController
}

// NewService 创建默认 npm MCP server 控制服务；它只响应显式 RPC 调用，不自动注入配置。
func NewService(servers defaultServerController) Service {
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

// StartSQLiteServer 透传到底层 MCP server 服务，写入或启用默认 sqlite stdio 配置。
func (s *service) StartSQLiteServer(ctx context.Context, req StartSQLiteServerRequest) (StartSQLiteServerResult, error) {
	if s == nil || s.servers == nil {
		return StartSQLiteServerResult{}, fmt.Errorf("mcp_server_npx: mcp server service is not configured")
	}
	result, err := s.servers.StartSQLiteServer(ctx, req)
	if err != nil {
		return StartSQLiteServerResult{}, err
	}
	return result, nil
}

// StopSQLiteServer 透传到底层 MCP server 服务，只关闭默认 sqlite 配置。
func (s *service) StopSQLiteServer(ctx context.Context, req StopSQLiteServerRequest) (StopSQLiteServerResult, error) {
	if s == nil || s.servers == nil {
		return StopSQLiteServerResult{}, fmt.Errorf("mcp_server_npx: mcp server service is not configured")
	}
	result, err := s.servers.StopSQLiteServer(ctx, req)
	if err != nil {
		return StopSQLiteServerResult{}, err
	}
	return result, nil
}

// StartPlaywrightServer 透传到底层 MCP server 服务，写入或启用默认 playwright stdio 配置。
func (s *service) StartPlaywrightServer(ctx context.Context, req StartPlaywrightServerRequest) (StartPlaywrightServerResult, error) {
	if s == nil || s.servers == nil {
		return StartPlaywrightServerResult{}, fmt.Errorf("mcp_server_npx: mcp server service is not configured")
	}
	result, err := s.servers.StartPlaywrightServer(ctx, req)
	if err != nil {
		return StartPlaywrightServerResult{}, err
	}
	return result, nil
}

// StopPlaywrightServer 透传到底层 MCP server 服务，只关闭默认 playwright 配置。
func (s *service) StopPlaywrightServer(ctx context.Context, req StopPlaywrightServerRequest) (StopPlaywrightServerResult, error) {
	if s == nil || s.servers == nil {
		return StopPlaywrightServerResult{}, fmt.Errorf("mcp_server_npx: mcp server service is not configured")
	}
	result, err := s.servers.StopPlaywrightServer(ctx, req)
	if err != nil {
		return StopPlaywrightServerResult{}, err
	}
	return result, nil
}
