package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/search"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	// grep 输出预算和提示文本保持稳定，便于模型按提示继续缩小查询。
	defaultSearchResults = 50
	maxSearchResults     = 50
	grepTruncatedHint    = "next: adjust max_results, narrow path/glob, refine query, or search a specific file"
	grepFuncRangeHint    = "next: file action=read_file pos=<file>:<func_start> limit=<func_end-func_start+1>"
)

// grepToolInput 是 grep 工具的外部入参，支持 path/paths/file_paths 三种路径写法。
type grepToolInput struct {
	Action        string   `json:"action"`
	Query         string   `json:"query,omitempty"`
	Path          string   `json:"path,omitempty"`
	Paths         []string `json:"-"`
	Glob          string   `json:"glob,omitempty"`
	Language      string   `json:"language,omitempty"`
	Regex         bool     `json:"regex,omitempty"`
	CaseSensitive *bool    `json:"case_sensitive,omitempty"`
	MaxResults    int      `json:"max_results,omitempty"`
}

// UnmarshalJSON 解码 grep 入参，并把兼容路径字段统一到 Path/Paths。
func (input *grepToolInput) UnmarshalJSON(raw []byte) error {
	var decoded struct {
		Action        string          `json:"action"`
		Query         string          `json:"query,omitempty"`
		Path          json.RawMessage `json:"path,omitempty"`
		Paths         json.RawMessage `json:"paths,omitempty"`
		FilePaths     json.RawMessage `json:"file_paths,omitempty"`
		Glob          string          `json:"glob,omitempty"`
		Language      string          `json:"language,omitempty"`
		Regex         bool            `json:"regex,omitempty"`
		CaseSensitive *bool           `json:"case_sensitive,omitempty"`
		MaxResults    int             `json:"max_results,omitempty"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	path, paths, err := decodeGrepPathInputs(
		grepPathInput{name: "path", raw: decoded.Path},
		grepPathInput{name: "paths", raw: decoded.Paths},
		grepPathInput{name: "file_paths", raw: decoded.FilePaths},
	)
	if err != nil {
		return err
	}
	*input = grepToolInput{
		Action:        decoded.Action,
		Query:         decoded.Query,
		Path:          path,
		Paths:         paths,
		Glob:          decoded.Glob,
		Language:      decoded.Language,
		Regex:         decoded.Regex,
		CaseSensitive: decoded.CaseSensitive,
		MaxResults:    decoded.MaxResults,
	}
	return nil
}

// grepPathInput 保存一个兼容路径字段的原始 JSON。
type grepPathInput struct {
	name string
	raw  json.RawMessage
}

// decodeGrepPathInputs 合并 path、paths 和 file_paths，保持单路径与多路径互斥表示。
func decodeGrepPathInputs(inputs ...grepPathInput) (string, []string, error) {
	paths := make([]string, 0, len(inputs))
	for _, input := range inputs {
		decoded, err := decodeGrepPathInput(input)
		if err != nil {
			return "", nil, err
		}
		paths = append(paths, decoded...)
	}
	switch len(paths) {
	case 0:
		return "", nil, nil
	case 1:
		return paths[0], nil, nil
	default:
		return "", paths, nil
	}
}

// decodeGrepPathInput 解码单个路径字段，接受字符串或字符串数组。
func decodeGrepPathInput(input grepPathInput) ([]string, error) {
	raw := bytes.TrimSpace(input.raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	var paths []string
	if err := json.Unmarshal(raw, &paths); err == nil {
		return normalizeGrepPathList(input.name, paths)
	}
	return nil, fmt.Errorf("%s must be a string or an array of strings", input.name)
}

// normalizeGrepPathList 校验路径数组，拒绝空数组和空字符串项。
func normalizeGrepPathList(name string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("%s array must contain at least one path", name)
	}
	normalized := make([]string, 0, len(paths))
	for index, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			return nil, fmt.Errorf("%s array contains empty path at index %d", name, index)
		}
		normalized = append(normalized, trimmed)
	}
	return normalized, nil
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
		root:     resolveRoot(cfg.WorkspaceRoot),
		registry: cfg.Registry,
	}
	return Handler(wrapToolHandler("grep", middleware.TierSlow, handler.handleGrep))
}

// handleGrep 分发 grep action，并统一做结果截断、日志和空响应处理。
func (h handlerBase) handleGrep(ctx context.Context, params json.RawMessage) (any, error) {
	input, err := decodeToolParams[grepToolInput](params, decodeLenient)
	if err != nil {
		return nil, err
	}
	limit := shared.ClampLimit(input.MaxResults, 1, maxSearchResults, defaultSearchResults)
	logGrepCallDecoded(input, limit)

	var (
		matches []search.SearchMatch
		runErr  error
	)
	if _, err := dispatchToolAction(ctx, "grep", input.Action, input, map[string]actionHandler[grepToolInput]{
		"text_search": func(ctx context.Context, input grepToolInput) (any, error) {
			matches, runErr = h.runGrepTextSearch(ctx, input, limit)
			return nil, runErr
		},
		"ast_search": func(ctx context.Context, input grepToolInput) (any, error) {
			root, roots, err := grepWorkspaceRoots(ctx)
			if err != nil {
				return nil, err
			}
			matches, runErr = search.SearchAST(ctx, search.ASTSearchOptions{
				Root:         root,
				Roots:        roots,
				Path:         input.Path,
				Paths:        input.Paths,
				Glob:         input.Glob,
				Query:        input.Query,
				Language:     input.Language,
				MaxResults:   limit,
				MaxFileBytes: maxReadFileBytes,
			})
			return nil, runErr
		},
	}); err != nil {
		return nil, err
	}

	filtered, total, truncated := filterAndLogGrepMatches(input, matches, limit)
	if len(filtered) == 0 {
		logGrepResponseEmpty(input, len(matches), total)
		return grepResponse{
			Data:    map[string]grepFileRows{},
			Total:   0,
			Showing: 0,
			Message: emptyGrepMessage(false),
		}, nil
	}
	resp := buildGrepResponse(filtered, total, truncated)
	if message := grepMessage(false, resp.DroppedForPayload); message != "" {
		resp.Message = message
	}
	finalizeGrepResponse(input, &resp)
	return resp, nil
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
	scope, ok := common.ToolScopeFromContext(ctx)
	if !ok || len(scope.WorkspaceRoots) == 0 {
		return "", nil, errors.New(staleWorkspaceRootMessage())
	}
	return toolWorkspaceRoots(ctx)
}

func staleWorkspaceRootMessage() string {
	return "mcp-lsp: stale workspace root; pass work_dir or _workspaceRoots"
}

func grepRuntimeFallbackWouldSearchOutsideRoots(ctx context.Context, input grepToolInput) bool {
	if !common.RuntimeWorkspaceScopeFallbackFromContext(ctx) {
		return false
	}
	if explicitToolWorkDirFromContext(ctx) {
		return false
	}
	if len(input.Paths) > 0 {
		return false
	}
	path := strings.TrimSpace(input.Path)
	return path != "" && !filepath.IsAbs(path)
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
		raw, err := json.Marshal(resp)
		if err != nil || len(raw) <= maxBytes {
			return
		}
		if !dropLastGrepRow(resp) {
			return
		}
		resp.Truncated = true
		resp.DroppedForPayload++
	}
}

// dropLastGrepRow 从行数最多的文件中移除一条匹配，用于把结构化响应压回预算内。
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

// ToPlainText 将结构化 grep 结果渲染为确定顺序的纯文本。
// 纯文本通道保留截断和下一步 hint，方便模型继续缩小查询。
func (r grepResponse) ToPlainText() string {
	if r.Total == 0 {
		return "No matches found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search Matches: showing %d of %d total\n", r.Showing, r.Total))
	if r.Message != "" {
		sb.WriteString(fmt.Sprintf("Message: %s\n", r.Message))
	}
	sb.WriteString("\n")

	// 固定文件输出顺序，避免 map 遍历导致快照和模型上下文抖动。
	var files []string
	for f := range r.Data {
		files = append(files, f)
	}
	sort.Strings(files)

	for _, file := range files {
		fr := r.Data[file]
		for _, row := range fr.Rows {
			r.formatGrepRow(&sb, file, row)
		}
	}

	if r.Truncated || r.DroppedForPayload > 0 {
		sb.WriteString("\nWarning: results were truncated due to limits or budget constraints.\n")
	}
	if r.Hint != "" {
		sb.WriteString(fmt.Sprintf("\nHint: %s\n", r.Hint))
	}

	return strings.TrimSpace(sb.String())
}

func (r grepResponse) formatGrepRow(sb *strings.Builder, file string, row []any) {
	if len(row) < 3 {
		return
	}
	lineVal := numericRowValue(row[0])
	colVal := numericRowValue(row[1])
	textVal, _ := row[2].(string)

	funcInfo := ""
	if len(row) >= 5 {
		fs := numericRowValue(row[3])
		fe := numericRowValue(row[4])
		if fs > 0 && fe >= fs {
			funcInfo = fmt.Sprintf(" [func L%d-L%d]", fs, fe)
		}
	}
	fmt.Fprintf(sb, "%s:%d:%d: %s%s\n", file, lineVal, colVal, textVal, funcInfo)
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
