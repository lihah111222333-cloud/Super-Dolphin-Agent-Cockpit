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

// StartPostgresServerRequest 是默认 postgres MCP server 的显式启动请求。
type StartPostgresServerRequest = contract.MCPPostgresServerStartRequest

// StartPostgresServerResult 返回配置写入位置以及本次是否新增配置。
type StartPostgresServerResult = contract.MCPPostgresServerStartResult

// StartSQLiteServerRequest 是默认 sqlite MCP server 的显式启动请求。
type StartSQLiteServerRequest = contract.MCPSQLiteServerStartRequest

// StartSQLiteServerResult 返回 sqlite MCP server 配置的写入和开启状态。
type StartSQLiteServerResult = contract.MCPSQLiteServerStartResult

// StopSQLiteServerRequest 是默认 sqlite MCP server 的显式关闭请求。
type StopSQLiteServerRequest = contract.MCPSQLiteServerStopRequest

// StopSQLiteServerResult 返回 sqlite MCP server 关闭后的状态。
type StopSQLiteServerResult = contract.MCPSQLiteServerStopResult

// StartPlaywrightServerRequest 是默认 playwright MCP server 的显式启动请求。
type StartPlaywrightServerRequest = contract.MCPPlaywrightServerStartRequest

// StartPlaywrightServerResult 返回 playwright MCP server 配置的写入和开启状态。
type StartPlaywrightServerResult = contract.MCPPlaywrightServerStartResult

// StopPlaywrightServerRequest 是默认 playwright MCP server 的显式关闭请求。
type StopPlaywrightServerRequest = contract.MCPPlaywrightServerStopRequest

// StopPlaywrightServerResult 返回 playwright MCP server 关闭后的状态。
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
