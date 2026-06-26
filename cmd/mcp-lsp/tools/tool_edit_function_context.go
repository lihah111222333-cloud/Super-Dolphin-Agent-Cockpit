package tools

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

// lookupFunctionContextWithLog 在 replace_range 成功后用 LSP 找回受影响函数。
// 这个步骤只用于回显上下文：manager 缺失、超时或找不到函数时记录日志并返回空，
// 不回滚已经确认落盘的编辑。
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
