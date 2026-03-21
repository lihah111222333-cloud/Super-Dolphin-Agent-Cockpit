package lspgui

import (
	"context"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func NewLSPGUIHandlers(svc Service) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"lsp/gui_file": rpc.StrictHandler(func(ctx context.Context, p fileParams) (any, error) {
			return svc.HandleFile(ctx, p)
		}),
		"lsp/gui_grep": rpc.StrictHandler(func(ctx context.Context, p grepParams) (any, error) {
			return svc.HandleGrep(ctx, p)
		}),
		"lsp/gui_structure": rpc.StrictHandler(func(ctx context.Context, p structureParams) (any, error) {
			return svc.HandleStructure(ctx, p)
		}),
		"lsp/gui_inspect": rpc.StrictHandler(func(ctx context.Context, p inspectParams) (any, error) {
			return svc.HandleInspect(ctx, p)
		}),
		"lsp/gui_xref": rpc.StrictHandler(func(ctx context.Context, p xrefParams) (any, error) {
			return svc.HandleXref(ctx, p)
		}),
	}}
}
