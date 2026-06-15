package tools

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/search"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func logGrepCallDecoded(input grepToolInput, limit int) {
	pkglogger.Info("mcp-lsp grep call decoded", grepLogAttrs(input,
		"requested_max_results", input.MaxResults,
		"effective_limit", limit,
		"case_sensitive", grepCaseSensitiveLogValue(input),
	)...)
}

// runGrepTextSearch 运行grep文本search。
func (handlerBase) runGrepTextSearch(ctx context.Context, input grepToolInput, limit int) ([]search.SearchMatch, error) {
	root, roots, err := toolWorkspaceRoots(ctx)
	if err != nil {
		pkglogger.Warn("mcp-lsp grep text_search workspace roots failed", grepLogAttrs(input,
			"error", err,
		)...)
		return nil, err
	}
	opts := search.TextSearchOptions{
		Root:          root,
		Roots:         roots,
		Path:          input.Path,
		Paths:         input.Paths,
		Glob:          input.Glob,
		Query:         input.Query,
		Regex:         input.Regex,
		CaseSensitive: input.CaseSensitive,
		MaxResults:    limit,
		MaxFileBytes:  maxReadFileBytes,
	}
	pkglogger.Info("mcp-lsp grep text_search started", grepLogAttrs(input,
		"root", root,
		"roots_count", len(roots),
		"limit", limit,
		"max_file_bytes", maxReadFileBytes,
	)...)
	matches, err := search.SearchText(ctx, opts)
	if err != nil {
		pkglogger.Warn("mcp-lsp grep text_search failed", grepLogAttrs(input,
			"root", root,
			"roots_count", len(roots),
			"error", err,
		)...)
		return nil, err
	}
	pkglogger.Info("mcp-lsp grep text_search returned", grepLogAttrs(input,
		"root", root,
		"roots_count", len(roots),
		"matches", len(matches),
	)...)
	if len(matches) > 0 {
		return matches, nil
	}
	pkglogger.Info("mcp-lsp grep text_search fallback started", grepLogAttrs(input,
		"root", root,
		"roots_count", len(roots),
	)...)
	fallbackMatches, err := searchSiblingWorkspaceOnRuntimeFallback(ctx, opts)
	if err != nil {
		pkglogger.Warn("mcp-lsp grep text_search fallback failed", grepLogAttrs(input,
			"root", root,
			"roots_count", len(roots),
			"error", err,
		)...)
		return nil, err
	}
	pkglogger.Info("mcp-lsp grep text_search fallback returned", grepLogAttrs(input,
		"root", root,
		"roots_count", len(roots),
		"matches", len(fallbackMatches),
		"used", len(fallbackMatches) > 0,
	)...)
	return fallbackMatches, nil
}

func filterAndLogGrepMatches(input grepToolInput, matches []search.SearchMatch, limit int) ([]search.SearchMatch, int, bool) {
	filtered, total, truncated := search.FilterAndCapSearchMatches(matches, limit)
	pkglogger.Info("mcp-lsp grep matches filtered", grepLogAttrs(input,
		"raw_matches", len(matches),
		"filtered_matches", len(filtered),
		"total", total,
		"truncated", truncated,
		"limit", limit,
	)...)
	return filtered, total, truncated
}

func logGrepResponseEmpty(input grepToolInput, rawMatches int, total int) {
	pkglogger.Info("mcp-lsp grep response empty", grepLogAttrs(input,
		"raw_matches", rawMatches,
		"total", total,
	)...)
}

func finalizeGrepResponse(input grepToolInput, resp *grepResponse) {
	if resp == nil {
		pkglogger.Warn("mcp-lsp grep response missing", grepLogAttrs(input)...)
		return
	}
	budget := middleware.ToolBudget("grep")
	showingBefore := resp.Showing
	capGrepResponseBytes(resp, budget)
	attrs := grepLogAttrs(input,
		"total", resp.Total,
		"showing_before_payload_cap", showingBefore,
		"showing", resp.Showing,
		"truncated", resp.Truncated,
		"dropped_for_payload", resp.DroppedForPayload,
		"budget_bytes", budget,
	)
	if resp.DroppedForPayload > 0 {
		pkglogger.Warn("mcp-lsp grep response payload capped", attrs...)
		return
	}
	pkglogger.Info("mcp-lsp grep response ready", attrs...)
}

func grepLogAttrs(input grepToolInput, attrs ...any) []any {
	base := []any{
		"tool", "grep",
		"action", input.Action,
		"query", input.Query,
		"path", input.Path,
		"paths_count", len(input.Paths),
		"glob", input.Glob,
		"regex", input.Regex,
	}
	return append(base, attrs...)
}

func grepCaseSensitiveLogValue(input grepToolInput) any {
	if input.CaseSensitive == nil {
		return nil
	}
	return *input.CaseSensitive
}
