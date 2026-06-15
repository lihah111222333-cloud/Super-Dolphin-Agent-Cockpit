package tools

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

// lookupFunctionContextWithLog 处理带日志的lookup函数上下文。
func (h EditHandler) lookupFunctionContextWithLog(ctx context.Context, manager lspmanager.Manager, path string, line int, content string, log *editStageLogger) functionContext {
	if manager == nil {
		log.Skipped("function_lookup", "manager_nil")
		return functionContext{}
	}
	if line <= 0 {
		log.Skipped("function_lookup", "line_unavailable")
		return functionContext{}
	}
	stage := log.Started("function_lookup", "line", line, "timeout_ms", editFunctionLookupTimeout.Milliseconds())
	lookupCtx, cancel := platformconfig.WithTimeout(ctx, editFunctionLookupTimeout)
	defer cancel()
	symbolLookup := manager.DocumentSymbol
	if bestEffort, ok := manager.(lspmanager.BestEffortDocumentSymbolManager); ok {
		symbolLookup = bestEffort.DocumentSymbolBestEffort
	}
	symbols, err := symbolLookup(lookupCtx, path)
	if err != nil {
		log.Failed("function_lookup", stage, err)
		return functionContext{}
	}
	start, end, ok := format.FindEnclosingFunction(symbols, line-1)
	if !ok {
		log.Completed("function_lookup", stage, "found", false)
		return functionContext{}
	}
	functionCtx := functionContext{Start: start, End: end, Body: functionBody(content, start, end)}
	log.Completed("function_lookup", stage,
		"found", true,
		"func_start", functionCtx.Start,
		"func_end", functionCtx.End,
	)
	return functionCtx
}
