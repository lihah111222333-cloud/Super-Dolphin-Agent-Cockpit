package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/search"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

const (
	// grep 输出预算和提示文本保持稳定，便于模型按提示继续缩小查询。
	defaultSearchResults = 50
	maxSearchResults     = 50
	maxSearchFileBytes   = 2 << 20
	grepTruncatedHint    = "next: adjust max_results, narrow paths/glob, refine query, or search a specific file"
	grepFuncRangeHint    = "next: inspect the returned file and line range with native cat/head tools"
)

// grepToolInput 是 grep 工具的外部入参，搜索范围只接受 paths 数组。
type grepToolInput struct {
	Action        string   `json:"action"`
	Query         string   `json:"query,omitempty"`
	Paths         []string `json:"paths,omitempty"`
	Glob          string   `json:"glob,omitempty"`
	ASTLanguage   string   `json:"ast_language,omitempty"`
	Regex         bool     `json:"regex,omitempty"`
	CaseSensitive *bool    `json:"case_sensitive,omitempty"`
	MaxResults    int      `json:"max_results,omitempty"`
}

// validateGrepPaths 校验 canonical paths 数组，空数组和空字符串项立即失败。
func validateGrepPaths(paths []string) error {
	if paths == nil {
		return nil
	}
	if len(paths) == 0 {
		return errors.New("paths must contain at least one search root")
	}
	for index, value := range paths {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("paths contains empty search root at index %d", index)
		}
	}
	return nil
}

// grepFileRows 是 grep 响应中单文件的表格化匹配行。
type grepFileRows struct {
	Cols []string `json:"cols"`
	Rows [][]any  `json:"rows"`
}

// grepResponse 是 grep 工具的结构化响应，包含截断和下一步提示。
type grepResponse struct {
	Data              map[string]grepFileRows `json:"data"`
	Total             int                     `json:"total"`
	Showing           int                     `json:"showing"`
	Truncated         bool                    `json:"truncated,omitempty"`
	DroppedForPayload int                     `json:"dropped_for_payload,omitempty"`
	RegexFallback     bool                    `json:"regex_fallback,omitempty"`
	Message           string                  `json:"message,omitempty"`
	Hint              string                  `json:"hint,omitempty"`
}

// NewGrepHandler 创建 grep 工具处理器，支持 text_search 和 ast_search。
func NewGrepHandler(cfg Config) Handler {
	handler := handlerBase{
		root:          resolveRoot(cfg.WorkspaceRoot),
		registry:      cfg.Registry,
		ensureASTGrep: cfg.EnsureASTGrep,
	}
	return Handler(wrapToolHandler("grep", middleware.TierSlow, handler.handleGrep))
}

// handleGrep 分发 grep action，并统一做结果截断、日志和空响应处理。
func (h handlerBase) handleGrep(ctx context.Context, params json.RawMessage) (any, error) {
	input, err := decodeToolParams[grepToolInput](params, decodeStrict)
	if err != nil {
		return nil, invalidGrepParams(err)
	}
	if err := validateGrepPaths(input.Paths); err != nil {
		return nil, invalidGrepParams(err)
	}
	if strings.TrimSpace(input.ASTLanguage) != "" && input.Action != "ast_search" {
		return nil, invalidGrepParams(errors.New("ast_language is only valid for ast_search"))
	}
	limit := shared.ClampLimit(input.MaxResults, 1, maxSearchResults, defaultSearchResults)
	logGrepCallDecoded(input, limit)

	var searchResult search.CountedSearchResult
	if _, err := dispatchToolAction(ctx, "grep", input.Action, input, map[string]actionHandler[grepToolInput]{
		"text_search": func(ctx context.Context, input grepToolInput) (any, error) {
			var err error
			searchResult, err = h.runGrepTextSearch(ctx, input, limit)
			return nil, err
		},
		"ast_search": func(ctx context.Context, input grepToolInput) (any, error) {
			root, roots, err := grepWorkspaceRoots(ctx)
			if err != nil {
				return nil, err
			}
			commandPath := ""
			if h.ensureASTGrep != nil {
				commandPath, err = h.ensureASTGrep(ctx)
				if err != nil {
					return nil, err
				}
			}
			searchResult, err = search.SearchASTCounted(ctx, search.ASTSearchOptions{
				Root:         root,
				Roots:        roots,
				Paths:        input.Paths,
				Glob:         input.Glob,
				Query:        input.Query,
				Language:     input.ASTLanguage,
				MaxResults:   limit,
				MaxFileBytes: maxSearchFileBytes,
				CommandPath:  commandPath,
			})
			if errors.Is(err, search.ErrInvalidASTLanguage) {
				return nil, invalidGrepASTLanguageParams(err)
			}
			return nil, err
		},
	}); err != nil {
		return nil, err
	}

	if len(searchResult.Matches) == 0 {
		logGrepResponseEmpty(input, 0, searchResult.Total)
		return grepResponse{
			Data:    map[string]grepFileRows{},
			Total:   searchResult.Total,
			Showing: 0,
			Message: emptyGrepMessage(false),
		}, nil
	}
	resp := buildGrepResponse(searchResult.Matches, searchResult.Total, searchResult.Truncated)
	if message := grepMessage(false, resp.DroppedForPayload); message != "" {
		resp.Message = message
	}
	finalizeGrepResponse(input, &resp)
	return resp, nil
}

// invalidGrepParams 统一返回不可重试的参数错误，并提示唯一 paths 契约。
func invalidGrepParams(err error) error {
	return common.NewCodedToolError(
		"invalid_params",
		err,
		false,
		"pass search roots only as paths=[\"dir\"]; a single root is a one-element array",
	)
}

// invalidGrepASTLanguageParams 返回 AST alias 值域或明确 glob 冲突的参数错误。
func invalidGrepASTLanguageParams(err error) error {
	return common.NewCodedToolError(
		"invalid_params",
		err,
		false,
		"use a registered ast_language alias and align it with any unambiguous glob",
	)
}

// grepMessage 组合 regex fallback 和 payload 截断提示。
func grepMessage(regexFallback bool, dropped int) string {
	parts := make([]string, 0, 2)
	if regexFallback {
		parts = append(parts, "regex parse failed; retried query as literal text")
	}
	if dropped > 0 {
		parts = append(parts, "payload truncated; narrow query/path/glob or reduce max_results")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ")
}

func grepWorkspaceRoots(ctx context.Context) (string, []string, error) {
	if !explicitToolWorkDirFromContext(ctx) && common.RuntimeWorkspaceScopeFallbackFromContext(ctx) {
		return "", nil, errors.New(staleWorkspaceRootMessage())
	}
	scope, ok := common.ToolScopeFromContext(ctx)
	if !ok || len(scope.WorkspaceRoots) == 0 {
		return "", nil, errors.New(staleWorkspaceRootMessage())
	}
	return toolWorkspaceRoots(ctx)
}

func staleWorkspaceRootMessage() string {
	return "mcp-lsp: stale workspace root; pass work_dir or _workspaceRoots"
}

func emptyGrepMessage(regexFallback bool) string {
	if regexFallback {
		return "regex parse failed; retried query as literal text; no matches found"
	}
	return "no matches found"
}

func capGrepResponseBytes(resp *grepResponse, maxBytes int) {
	for {
		ensureGrepResponseHint(resp)
		resp.Message = grepMessage(resp.RegexFallback, resp.DroppedForPayload)
		if len([]byte(resp.ToPlainText())) <= maxBytes {
			return
		}
		if !dropLastGrepRow(resp) {
			return
		}
		resp.Truncated = true
		resp.DroppedForPayload++
	}
}

// dropLastGrepRow 从行数最多的文件中移除一条匹配，用于把最终文本压回预算内。
// 如果该文件只剩一行，直接移除整个文件块，保证 showing 计数同步下降。
func dropLastGrepRow(resp *grepResponse) bool {
	var maxFile string
	var maxRows int
	for file, fr := range resp.Data {
		if len(fr.Rows) > maxRows {
			maxRows = len(fr.Rows)
			maxFile = file
		}
	}
	if maxFile == "" {
		return false
	}
	fr := resp.Data[maxFile]
	if len(fr.Rows) <= 1 {
		delete(resp.Data, maxFile)
	} else {
		fr.Rows = fr.Rows[:len(fr.Rows)-1]
		resp.Data[maxFile] = fr
	}
	resp.Showing--
	if resp.Showing < 0 {
		resp.Showing = 0
	}
	return true
}

// buildGrepResponse 把搜索匹配按文件聚合成稳定的结构化响应。
// 当任一匹配带函数范围时，所有文件统一补齐 func_start/func_end 列以保持表格 schema 一致。
func buildGrepResponse(matches []search.SearchMatch, total int, truncated bool) grepResponse {
	data := make(map[string]grepFileRows, len(matches))
	hasFuncRanges := false
	for _, match := range matches {
		row := []any{match.Line, match.Col, match.Text}
		if match.FuncStart > 0 && match.FuncEnd >= match.FuncStart {
			row = append(row, match.FuncStart, match.FuncEnd)
			hasFuncRanges = true
		}
		block := data[match.File]
		if len(block.Cols) == 0 {
			block.Cols = grepRowCols(hasFuncRanges)
		}
		block.Rows = append(block.Rows, row)
		data[match.File] = block
	}
	// 所有匹配遍历完后才能知道是否存在函数范围列；统一回填列头和空值，避免同一响应内 schema 漂移。
	for path, block := range data {
		block.Cols = grepRowCols(hasFuncRanges)
		if hasFuncRanges {
			block.Rows = padGrepRows(block.Rows, len(block.Cols))
		}
		data[path] = block
	}
	return grepResponse{
		Data:      data,
		Total:     total,
		Showing:   len(matches),
		Truncated: truncated,
		Hint:      grepHint(truncated, hasFuncRanges),
	}
}

func ensureGrepResponseHint(resp *grepResponse) {
	if resp == nil {
		return
	}
	if hint := grepHint(resp.Truncated, grepResponseHasFuncRanges(*resp)); hint != "" {
		resp.Hint = hint
	}
}

func grepHint(truncated bool, hasFuncRanges bool) string {
	parts := make([]string, 0, 2)
	if truncated {
		parts = append(parts, grepTruncatedHint)
	}
	if hasFuncRanges {
		parts = append(parts, grepFuncRangeHint)
	}
	return strings.Join(parts, "; ")
}

func grepResponseHasFuncRanges(resp grepResponse) bool {
	for _, block := range resp.Data {
		if slices.Contains(block.Cols, "func_start") {
			return true
		}
	}
	return false
}

// grepRowCols 返回与 buildGrepResponse 实际行宽一致的列头。
// 没有任何命中携带函数范围时不声明 func_start/func_end，避免调用方读取不存在的字段。
func grepRowCols(includeFuncRange bool) []string {
	base := []string{"line", "col", "text"}
	if includeFuncRange {
		return append(base, "func_start", "func_end")
	}
	return base
}

func padGrepRows(rows [][]any, width int) [][]any {
	for idx := range rows {
		for len(rows[idx]) < width {
			rows[idx] = append(rows[idx], nil)
		}
	}
	return rows
}

// ToPlainText 将 grep 结果渲染为确定顺序的紧凑行协议。
// 纯文本通道保留截断和下一步 hint，方便模型继续缩小查询。
func (r grepResponse) ToPlainText() string {
	lines := []string{lineprotocol.HeaderLine(r.Total, r.Showing, r.Showing < r.Total, "match")}

	// 固定文件输出顺序，避免 map 遍历导致快照和模型上下文抖动。
	var files []string
	for f := range r.Data {
		files = append(files, f)
	}
	sort.Strings(files)

	for _, file := range files {
		fr := r.Data[file]
		for _, row := range fr.Rows {
			if record := r.formatGrepRow(file, row); record != "" {
				lines = append(lines, record)
			}
		}
	}
	if r.Message != "" {
		lines = append(lines, lineprotocol.TextRecord("MESSAGE", r.Message))
	}
	if r.Showing < r.Total {
		lines = append(lines, lineprotocol.TextRecord("WARNING", "results-truncated"))
	}
	if r.Hint != "" {
		lines = append(lines, lineprotocol.TextRecord("HINT", r.Hint))
	}
	return strings.Join(lines, "\n")
}

func (r grepResponse) formatGrepRow(file string, row []any) string {
	if len(row) < 3 {
		return ""
	}
	lineVal := numericRowValue(row[0])
	colVal := numericRowValue(row[1])
	textVal, _ := row[2].(string)
	fields := []lineprotocol.Field{
		{Key: "file", Value: file},
		{Key: "line", Value: fmt.Sprint(lineVal)},
		{Key: "col", Value: fmt.Sprint(colVal)},
		{Key: "text", Value: textVal},
	}
	if len(row) >= 5 {
		fs := numericRowValue(row[3])
		fe := numericRowValue(row[4])
		if fs > 0 && fe >= fs {
			fields = append(fields,
				lineprotocol.Field{Key: "func_start", Value: fmt.Sprint(fs)},
				lineprotocol.Field{Key: "func_end", Value: fmt.Sprint(fe)},
			)
		}
	}
	return lineprotocol.FieldsRecord("ROW", fields...)
}

// numericRowValue 兼容首次构建的 int 和 JSON 往返后的 float64。
// 预算压缩可能把响应降级成 map[string]any，缺少这个转换会在纯文本里静默输出 L0:0。
func numericRowValue(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	default:
		return 0
	}
}
