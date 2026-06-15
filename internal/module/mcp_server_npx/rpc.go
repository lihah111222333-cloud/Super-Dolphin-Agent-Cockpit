package mcpservernpx

import (
	"context"

	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

// NewHandlers 注册默认 npx MCP server 的显式启动 RPC。
func NewHandlers(svc Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"mcpServer/postgres/start": platformrpc.StrictHandler(startPostgresServerHandler(svc)),
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
