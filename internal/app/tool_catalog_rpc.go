package app

import (
	"context"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
)

const toolCatalogListMethod = "toolbridge/tools/list"

type toolCatalogLister interface {
	ListToolCatalog(context.Context, string) ([]toolbridge.ToolCatalogEntry, error)
}

type toolCatalogListRequest struct {
	CWD string `json:"cwd"`
}

type toolCatalogListResponse struct {
	Tools []toolbridge.ToolCatalogEntry `json:"tools"`
}

// newToolCatalogHandlers 把生产 toolbridge handler 适配为 RPC handler map。
func newToolCatalogHandlers(toolbridgeHandler *toolbridge.Handler) platformrpc.HandlerMapResult {
	return toolCatalogHandlers(toolbridgeHandler)
}

// toolCatalogHandlers 注册严格的工作区工具目录查询方法。
func toolCatalogHandlers(lister toolCatalogLister) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		toolCatalogListMethod: platformrpc.StrictHandler(
			func(ctx context.Context, req toolCatalogListRequest) (toolCatalogListResponse, error) {
				cwd := strings.TrimSpace(req.CWD)
				if cwd == "" {
					return toolCatalogListResponse{}, platformrpc.ErrInvalidParams(
						"toolbridge/tools/list: cwd is required",
					)
				}
				if lister == nil {
					return toolCatalogListResponse{}, platformrpc.ErrInvalidState(
						"toolbridge tool catalog is not configured",
					)
				}
				tools, err := lister.ListToolCatalog(ctx, cwd)
				if err != nil {
					return toolCatalogListResponse{}, err
				}
				if tools == nil {
					tools = []toolbridge.ToolCatalogEntry{}
				}
				return toolCatalogListResponse{Tools: tools}, nil
			},
		),
	}}
}
