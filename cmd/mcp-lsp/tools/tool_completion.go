package tools

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

type completionParams struct {
	Pos        string `json:"pos"`
	LanguageID string `json:"language_id,omitempty"`
	Verbosity  string `json:"verbosity"`
	MaxResults int    `json:"max_results"`
}

func NewCompletionHandler(registry lspmanager.Registry) ToolHandler {
	return newManagerTool("completion", middleware.TierFast, registry, decodeStrict, func(ctx context.Context, registry lspmanager.Registry, req completionParams) (any, error) {
		filePath, position, err := resolveFilePositionRequest(ctx, filePositionParams{
			Pos:        req.Pos,
			LanguageID: req.LanguageID,
		})
		if err != nil {
			return nil, err
		}
		manager, err := managerForFile(ctx, registry, filePath, req.LanguageID)
		if err != nil {
			return nil, err
		}
		verbosity := format.NormalizeVerbosity(req.Verbosity)
		limit := format.CompletionLimit(req.MaxResults, verbosity)
		result, err := manager.Completion(ctx, filePath, position)
		if err != nil {
			return nil, err
		}
		if result == nil || len(result.Items) == 0 {
			return emptyListEnvelope{
				Success: true,
				Data:    []any{},
				Meta:    resultMeta{Count: 0, Message: "no completions"},
			}, nil
		}
		total := len(result.Items)
		items := limitSlice(result.Items, limit)
		return renderByVerbosity(items, total, verbosity,
			func(items []protocol.CompletionItem) any { return items },
			func(items []protocol.CompletionItem, total int) any {
				return format.NewCompactList(format.CompactCompletionItems(items), total)
			},
		), nil
	})
}
