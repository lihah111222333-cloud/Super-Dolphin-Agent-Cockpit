package tools

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/format"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/gopls"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
)

type completionParams struct {
	FilePath   string `json:"file_path"`
	Line       int    `json:"line"`
	Column     int    `json:"column"`
	Verbosity  string `json:"verbosity"`
	MaxResults int    `json:"max_results"`
}

func NewCompletionHandler(manager gopls.Manager) ToolHandler {
	return newManagerTool("lsp_completion", middleware.TierFast, manager, decodeStrict, func(ctx context.Context, manager gopls.Manager, req completionParams) (any, error) {
		filePath, position, err := resolveFilePositionRequest(filePositionParams{
			FilePath: req.FilePath,
			Line:     req.Line,
			Column:   req.Column,
		})
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
			return "no completions", nil
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
