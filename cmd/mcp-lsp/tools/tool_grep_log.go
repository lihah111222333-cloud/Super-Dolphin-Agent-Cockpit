package tools

import (
	"context"
	"errors"
	"regexp/syntax"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/search"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// logGrepCallDecoded 记录 grep 入参解码后的有效限制，方便排查路径和大小写选择。
func logGrepCallDecoded(input grepToolInput, limit int) {
	pkglogger.Info("mcp-lsp grep call decoded", grepLogAttrs(input,
		"requested_max_results", input.MaxResults,
		"effective_limit", limit,
		"case_sensitive", grepCaseSensitiveLogValue(input),
	)...)
}

// runGrepTextSearch 只在可信 workspace roots 内运行文本搜索。
// runtime fallback 曾经会读兄弟 worktree；现在遇到该场景直接报 stale-root，要求调用方传入明确作用域。
func (handlerBase) runGrepTextSearch(ctx context.Context, input grepToolInput, limit int) (search.CountedSearchResult, error) {
	root, roots, err := grepWorkspaceRoots(ctx)
	if err != nil {
		pkglogger.Warn("mcp-lsp grep text_search workspace roots failed", grepLogAttrs(input,
			"error", err,
		)...)
		return search.CountedSearchResult{}, err
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
	result, err := search.SearchTextCounted(ctx, opts)
	if err != nil {
		pkglogger.Warn("mcp-lsp grep text_search failed", grepLogAttrs(input,
			"root", root,
			"roots_count", len(roots),
			"error", err,
		)...)
		if _, ok := errors.AsType[*syntax.Error](err); ok {
			return search.CountedSearchResult{}, common.NewCodedToolError(
				"invalid_params", err, false, "fix-regex-syntax-or-set-regex=false-for-literal-search",
			)
		}
		return search.CountedSearchResult{}, err
	}
	pkglogger.Info("mcp-lsp grep text_search returned", grepLogAttrs(input,
		"root", root,
		"roots_count", len(roots),
		"matches", len(result.Matches),
		"total", result.Total,
	)...)
	if len(result.Matches) > 0 {
		return result, nil
	}
	if grepRuntimeFallbackWouldSearchOutsideRoots(ctx, input) {
		err := errors.New(staleWorkspaceRootMessage())
		pkglogger.Warn("mcp-lsp grep text_search stale workspace root", grepLogAttrs(input,
			"root", root,
			"roots_count", len(roots),
			"error", err,
		)...)
		return search.CountedSearchResult{}, err
	}
	return result, nil
}

// logGrepResponseEmpty 记录空结果，区分原始无匹配和过滤后为空。
func logGrepResponseEmpty(input grepToolInput, rawMatches int, total int) {
	pkglogger.Info("mcp-lsp grep response empty", grepLogAttrs(input,
		"raw_matches", rawMatches,
		"total", total,
	)...)
}

// finalizeGrepResponse 按工具预算裁剪 grep 响应并记录最终可见数量。
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

// grepLogAttrs 统一 grep 日志字段，避免每个阶段手写 path/query 信息。
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

// grepCaseSensitiveLogValue 返回大小写配置的三态值，nil 表示使用默认 smart-case。
func grepCaseSensitiveLogValue(input grepToolInput) any {
	if input.CaseSensitive == nil {
		return nil
	}
	return *input.CaseSensitive
}
