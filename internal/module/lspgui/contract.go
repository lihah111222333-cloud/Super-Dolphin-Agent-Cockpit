package lspgui

import "context"

type Service interface {
	HandleFile(ctx context.Context, p fileParams) (any, error)
	HandleGrep(ctx context.Context, p grepParams) (any, error)
	HandleStructure(ctx context.Context, p structureParams) (any, error)
	HandleInspect(ctx context.Context, p inspectParams) (any, error)
	HandleXref(ctx context.Context, p xrefParams) (any, error)
}
