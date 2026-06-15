package mcpserver

import (
	"context"
	"errors"

	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

// NewHandlers 创建处理器。
func NewHandlers(svc Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"mcpServer/add":    platformrpc.StrictHandler(addServersHandler(svc)),
		"mcpServer/list":   platformrpc.StrictHandler(listServersHandler(svc)),
		"mcpServer/tools":  platformrpc.StrictHandler(listServerToolsHandler(svc)),
		"mcpServer/delete": platformrpc.StrictHandler(deleteServerHandler(svc)),
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

// mcpServerRPCError 把模块内错误转换为 RPC 错误类型，保证参数问题和远端状态问题不会混在一起。
func mcpServerRPCError(err error) error {
	switch {
	case errors.Is(err, errMissingMCPServers),
		errors.Is(err, errMissingServerName),
		errors.Is(err, errDuplicateServerName),
		errors.Is(err, errMissingServerTransport),
		errors.Is(err, errUnsupportedTransport),
		errors.Is(err, errMissingServerURL),
		errors.Is(err, errInvalidServerURL),
		errors.Is(err, errMissingHeaderName),
		errors.Is(err, errMissingHeaderValue),
		errors.Is(err, errInvalidConfigDocument):
		return platformrpc.ErrInvalidParams(err.Error())
	case errors.Is(err, errMCPServerStoreNotConfigured):
		return platformrpc.ErrInvalidState(err.Error())
	case errors.Is(err, errMCPServerToolsRequestFailed),
		errors.Is(err, errInvalidToolsResponse):
		return platformrpc.ErrInvalidState(err.Error())
	case errors.Is(err, errServerNotFound):
		return platformrpc.ErrNotFound(err.Error())
	case errors.Is(err, errServerAlreadyExists):
		return platformrpc.ErrConflict(err.Error())
	default:
		return err
	}
}
