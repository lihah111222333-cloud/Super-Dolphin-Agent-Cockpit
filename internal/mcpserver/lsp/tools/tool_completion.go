package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/format"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/gopls"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/middleware"
)

type completionParams struct {
	FilePath   string `json:"file_path"`
	Line       int    `json:"line"`
	Column     int    `json:"column"`
	Verbosity  string `json:"verbosity"`
	MaxResults int    `json:"max_results"`
}

func NewCompletionHandler(manager gopls.Manager) ToolHandler {
	if manager == nil {
		return missingManagerHandler()
	}
	return ToolHandler(wrapToolHandler("lsp_completion", middleware.TierFast, func(ctx context.Context, params json.RawMessage) (any, error) {
		req, err := decodeParams[completionParams](params)
		if err != nil {
			return nil, err
		}
		filePath, position, err := requireFilePosition(filePositionParams{
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
		if verbosity == format.VerbosityFull {
			return items, nil
		}
		return format.NewCompactList(format.CompactCompletionItems(items), total), nil
	}))
}

var _ = fmt.Sprintf
