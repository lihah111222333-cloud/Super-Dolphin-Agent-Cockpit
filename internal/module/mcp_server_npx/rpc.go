package mcpservernpx

import (
	"context"

	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

type startPostgresServerPublicResult struct {
	ConfigPath string `json:"configPath"`
	ServerName string `json:"serverName"`
	Added      bool   `json:"added"`
}

type startSQLiteServerPublicResult struct {
	ConfigPath string `json:"configPath"`
	ServerName string `json:"serverName"`
	Added      bool   `json:"added"`
	Enabled    bool   `json:"enabled"`
}

type startPlaywrightServerPublicResult struct {
	ConfigPath string `json:"configPath"`
	ServerName string `json:"serverName"`
	Added      bool   `json:"added"`
	Enabled    bool   `json:"enabled"`
}

func toStartPostgresServerPublicResult(result StartPostgresServerResult) startPostgresServerPublicResult {
	return startPostgresServerPublicResult{
		ConfigPath: result.ConfigPath,
		ServerName: result.ServerName,
		Added:      result.Added,
	}
}

func toStartSQLiteServerPublicResult(result StartSQLiteServerResult) startSQLiteServerPublicResult {
	return startSQLiteServerPublicResult{
		ConfigPath: result.ConfigPath,
		ServerName: result.ServerName,
		Added:      result.Added,
		Enabled:    result.Enabled,
	}
}

func toStartPlaywrightServerPublicResult(result StartPlaywrightServerResult) startPlaywrightServerPublicResult {
	return startPlaywrightServerPublicResult{
		ConfigPath: result.ConfigPath,
		ServerName: result.ServerName,
		Added:      result.Added,
		Enabled:    result.Enabled,
	}
}

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

func startPostgresServerHandler(svc Service) func(context.Context, StartPostgresServerRequest) (startPostgresServerPublicResult, error) {
	return func(ctx context.Context, req StartPostgresServerRequest) (startPostgresServerPublicResult, error) {
		if svc == nil {
			return startPostgresServerPublicResult{}, platformrpc.ErrInvalidState("mcp postgres server service is not configured")
		}
		result, err := svc.StartPostgresServer(ctx, req)
		if err != nil {
			return startPostgresServerPublicResult{}, err
		}
		return toStartPostgresServerPublicResult(result), nil
	}
}

func startSQLiteServerHandler(svc Service) func(context.Context, StartSQLiteServerRequest) (startSQLiteServerPublicResult, error) {
	return func(ctx context.Context, req StartSQLiteServerRequest) (startSQLiteServerPublicResult, error) {
		if svc == nil {
			return startSQLiteServerPublicResult{}, platformrpc.ErrInvalidState("mcp sqlite server service is not configured")
		}
		result, err := svc.StartSQLiteServer(ctx, req)
		if err != nil {
			return startSQLiteServerPublicResult{}, err
		}
		return toStartSQLiteServerPublicResult(result), nil
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

func startPlaywrightServerHandler(svc Service) func(context.Context, StartPlaywrightServerRequest) (startPlaywrightServerPublicResult, error) {
	return func(ctx context.Context, req StartPlaywrightServerRequest) (startPlaywrightServerPublicResult, error) {
		if svc == nil {
			return startPlaywrightServerPublicResult{}, platformrpc.ErrInvalidState("mcp playwright server service is not configured")
		}
		result, err := svc.StartPlaywrightServer(ctx, req)
		if err != nil {
			return startPlaywrightServerPublicResult{}, err
		}
		return toStartPlaywrightServerPublicResult(result), nil
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
