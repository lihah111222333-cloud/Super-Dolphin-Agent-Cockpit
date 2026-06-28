package mcpserver

import (
	"context"
	"errors"

	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

// NewHandlers 注册 MCP server 管理相关的 RPC 处理器。
func NewHandlers(svc Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"mcpServer/add":              platformrpc.StrictHandler(addServersHandler(svc)),
		"mcpServer/list":             platformrpc.StrictHandler(listServersHandler(svc)),
		"mcpServer/tools":            platformrpc.StrictHandler(listServerToolsHandler(svc)),
		"mcpServer/postgres/start":   platformrpc.StrictHandler(startPostgresServerHandler(svc)),
		"mcpServer/sqlite/start":     platformrpc.StrictHandler(startSQLiteServerHandler(svc)),
		"mcpServer/sqlite/stop":      platformrpc.StrictHandler(stopSQLiteServerHandler(svc)),
		"mcpServer/playwright/start": platformrpc.StrictHandler(startPlaywrightServerHandler(svc)),
		"mcpServer/playwright/stop":  platformrpc.StrictHandler(stopPlaywrightServerHandler(svc)),
		"mcpServer/delete":           platformrpc.StrictHandler(deleteServerHandler(svc)),
	}}
}

func addServersHandler(svc Service) func(context.Context, AddServersRequest) (AddServersResult, error) {
	return func(ctx context.Context, req AddServersRequest) (AddServersResult, error) {
		if svc == nil {
			return AddServersResult{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.AddServers(ctx, req)
		if err != nil {
			return AddServersResult{}, mcpServerRPCError(err)
		}
		return result, nil
	}
}

func listServersHandler(svc Service) func(context.Context, struct{}) (ListServersResult, error) {
	return func(ctx context.Context, _ struct{}) (ListServersResult, error) {
		if svc == nil {
			return ListServersResult{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.ListServers(ctx)
		if err != nil {
			return ListServersResult{}, mcpServerRPCError(err)
		}
		return result, nil
	}
}

func listServerToolsHandler(svc Service) func(context.Context, ListServerToolsRequest) (ListServerToolsResult, error) {
	return func(ctx context.Context, req ListServerToolsRequest) (ListServerToolsResult, error) {
		if svc == nil {
			return ListServerToolsResult{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.ListServerTools(ctx, req)
		if err != nil {
			return ListServerToolsResult{}, mcpServerRPCError(err)
		}
		return result, nil
	}
}

func startPostgresServerHandler(svc Service) func(context.Context, StartPostgresServerRequest) (StartPostgresServerResult, error) {
	return func(ctx context.Context, req StartPostgresServerRequest) (StartPostgresServerResult, error) {
		if svc == nil {
			return StartPostgresServerResult{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.StartPostgresServer(ctx, req)
		if err != nil {
			return StartPostgresServerResult{}, mcpServerRPCError(err)
		}
		return result, nil
	}
}

func startSQLiteServerHandler(svc Service) func(context.Context, StartSQLiteServerRequest) (StartSQLiteServerResult, error) {
	return func(ctx context.Context, req StartSQLiteServerRequest) (StartSQLiteServerResult, error) {
		if svc == nil {
			return StartSQLiteServerResult{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.StartSQLiteServer(ctx, req)
		if err != nil {
			return StartSQLiteServerResult{}, mcpServerRPCError(err)
		}
		return result, nil
	}
}

func stopSQLiteServerHandler(svc Service) func(context.Context, StopSQLiteServerRequest) (StopSQLiteServerResult, error) {
	return func(ctx context.Context, req StopSQLiteServerRequest) (StopSQLiteServerResult, error) {
		if svc == nil {
			return StopSQLiteServerResult{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.StopSQLiteServer(ctx, req)
		if err != nil {
			return StopSQLiteServerResult{}, mcpServerRPCError(err)
		}
		return result, nil
	}
}

func startPlaywrightServerHandler(svc Service) func(context.Context, StartPlaywrightServerRequest) (StartPlaywrightServerResult, error) {
	return func(ctx context.Context, req StartPlaywrightServerRequest) (StartPlaywrightServerResult, error) {
		if svc == nil {
			return StartPlaywrightServerResult{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.StartPlaywrightServer(ctx, req)
		if err != nil {
			return StartPlaywrightServerResult{}, mcpServerRPCError(err)
		}
		return result, nil
	}
}

func stopPlaywrightServerHandler(svc Service) func(context.Context, StopPlaywrightServerRequest) (StopPlaywrightServerResult, error) {
	return func(ctx context.Context, req StopPlaywrightServerRequest) (StopPlaywrightServerResult, error) {
		if svc == nil {
			return StopPlaywrightServerResult{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.StopPlaywrightServer(ctx, req)
		if err != nil {
			return StopPlaywrightServerResult{}, mcpServerRPCError(err)
		}
		return result, nil
	}
}

func deleteServerHandler(svc Service) func(context.Context, DeleteServerRequest) (DeleteServerResult, error) {
	return func(ctx context.Context, req DeleteServerRequest) (DeleteServerResult, error) {
		if svc == nil {
			return DeleteServerResult{}, platformrpc.ErrInvalidState("mcp server service is not configured")
		}
		result, err := svc.DeleteServer(ctx, req)
		if err != nil {
			return DeleteServerResult{}, mcpServerRPCError(err)
		}
		return result, nil
	}
}

// mcpServerRPCError 将模块内部错误转换为对应的 RPC 错误类型，确保参数错误和远端状态错误分类清晰。
func mcpServerRPCError(err error) error {
	switch {
	case errors.Is(err, errMissingMCPServers),
		errors.Is(err, errMissingServerName),
		errors.Is(err, errDuplicateServerName),
		errors.Is(err, errMissingServerTransport),
		errors.Is(err, errUnsupportedTransport),
		errors.Is(err, errMissingServerURL),
		errors.Is(err, errInvalidServerURL),
		errors.Is(err, errMissingServerCommand),
		errors.Is(err, errUnsupportedStdioCommand),
		errors.Is(err, errMissingServerArg),
		errors.Is(err, errMissingServerEnvName),
		errors.Is(err, errMissingServerEnvValue),
		errors.Is(err, errMissingHeaderName),
		errors.Is(err, errMissingHeaderValue),
		errors.Is(err, errInvalidConfigDocument):
		return platformrpc.ErrInvalidParams(err.Error())
	case errors.Is(err, errMCPServerStoreNotConfigured):
		return platformrpc.ErrInvalidState(err.Error())
	case errors.Is(err, errMCPServerToolsRequestFailed),
		errors.Is(err, errInvalidToolsResponse),
		errors.Is(err, errPostgresInstallerMissing):
		return platformrpc.ErrInvalidState(err.Error())
	case errors.Is(err, errServerNotFound):
		return platformrpc.ErrNotFound(err.Error())
	case errors.Is(err, errServerAlreadyExists):
		return platformrpc.ErrConflict(err.Error())
	default:
		return err
	}
}
