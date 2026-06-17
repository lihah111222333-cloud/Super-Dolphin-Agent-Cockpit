package mcpservernpx

import (
	"context"

	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

// NewHandlers 注册默认 npm MCP server 的显式启动 RPC。
func NewHandlers(svc Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"mcpServer/postgres/start":   platformrpc.StrictHandler(startPostgresServerHandler(svc)),
		"mcpServer/sqlite/start":     platformrpc.StrictHandler(startSQLiteServerHandler(svc)),
		"mcpServer/sqlite/stop":      platformrpc.StrictHandler(stopSQLiteServerHandler(svc)),
		"mcpServer/playwright/start": platformrpc.StrictHandler(startPlaywrightServerHandler(svc)),
		"mcpServer/playwright/stop":  platformrpc.StrictHandler(stopPlaywrightServerHandler(svc)),
	}}
}

func startPostgresServerHandler(svc Service) func(context.Context, StartPostgresServerRequest) (StartPostgresServerResult, error) {
	return func(ctx context.Context, req StartPostgresServerRequest) (StartPostgresServerResult, error) {
		if svc == nil {
			return StartPostgresServerResult{}, platformrpc.ErrInvalidState("mcp postgres server service is not configured")
		}
		result, err := svc.StartPostgresServer(ctx, req)
		if err != nil {
			return StartPostgresServerResult{}, err
		}
		return result, nil
	}
}

func startSQLiteServerHandler(svc Service) func(context.Context, StartSQLiteServerRequest) (StartSQLiteServerResult, error) {
	return func(ctx context.Context, req StartSQLiteServerRequest) (StartSQLiteServerResult, error) {
		if svc == nil {
			return StartSQLiteServerResult{}, platformrpc.ErrInvalidState("mcp sqlite server service is not configured")
		}
		result, err := svc.StartSQLiteServer(ctx, req)
		if err != nil {
			return StartSQLiteServerResult{}, err
		}
		return result, nil
	}
}

func stopSQLiteServerHandler(svc Service) func(context.Context, StopSQLiteServerRequest) (StopSQLiteServerResult, error) {
	return func(ctx context.Context, req StopSQLiteServerRequest) (StopSQLiteServerResult, error) {
		if svc == nil {
			return StopSQLiteServerResult{}, platformrpc.ErrInvalidState("mcp sqlite server service is not configured")
		}
		result, err := svc.StopSQLiteServer(ctx, req)
		if err != nil {
			return StopSQLiteServerResult{}, err
		}
		return result, nil
	}
}

func startPlaywrightServerHandler(svc Service) func(context.Context, StartPlaywrightServerRequest) (StartPlaywrightServerResult, error) {
	return func(ctx context.Context, req StartPlaywrightServerRequest) (StartPlaywrightServerResult, error) {
		if svc == nil {
			return StartPlaywrightServerResult{}, platformrpc.ErrInvalidState("mcp playwright server service is not configured")
		}
		result, err := svc.StartPlaywrightServer(ctx, req)
		if err != nil {
			return StartPlaywrightServerResult{}, err
		}
		return result, nil
	}
}

func stopPlaywrightServerHandler(svc Service) func(context.Context, StopPlaywrightServerRequest) (StopPlaywrightServerResult, error) {
	return func(ctx context.Context, req StopPlaywrightServerRequest) (StopPlaywrightServerResult, error) {
		if svc == nil {
			return StopPlaywrightServerResult{}, platformrpc.ErrInvalidState("mcp playwright server service is not configured")
		}
		result, err := svc.StopPlaywrightServer(ctx, req)
		if err != nil {
			return StopPlaywrightServerResult{}, err
		}
		return result, nil
	}
}
