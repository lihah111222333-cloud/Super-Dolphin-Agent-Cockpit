package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/search"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	defaultSearchResults = 50
	maxSearchResults     = 50
)

type grepToolInput struct {
	Action        string `json:"action"`
	Query         string `json:"query,omitempty"`
	Path          string `json:"path,omitempty"`
	Glob          string `json:"glob,omitempty"`
	Language      string `json:"language,omitempty"`
	Regex         bool   `json:"regex,omitempty"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	MaxResults    int    `json:"max_results,omitempty"`
}

type grepFileRows struct {
	Cols []string `json:"cols"`
	Rows [][]any  `json:"rows"`
}

type grepResponse struct {
	Files             map[string]grepFileRows `json:"files"`
	Total             int                     `json:"total"`
	Showing           int                     `json:"showing"`
	Truncated         bool                    `json:"truncated,omitempty"`
	DroppedForPayload int                     `json:"dropped_for_payload,omitempty"`
	RegexFallback     bool                    `json:"regex_fallback,omitempty"`
	Message           string                  `json:"message,omitempty"`
	Hint              string                  `json:"hint,omitempty"`
}

func NewGrepHandler(cfg Config) Handler {
	handler := handlerBase{
		root:     resolveRoot(cfg.WorkspaceRoot),
		registry: cfg.Registry,
	}
	return Handler(wrapToolHandler("grep", middleware.TierSlow, handler.handleGrep))
}

func (h handlerBase) handleGrep(ctx context.Context, params json.RawMessage) (any, error) {
	input, err := decodeToolParams[grepToolInput](params, decodeLenient)
	if err != nil {
		return nil, err
	}
	limit := shared.ClampLimit(input.MaxResults, 1, maxSearchResults, defaultSearchResults)

	var (
		matches []search.SearchMatch
		runErr  error
	)
	if _, err := dispatchToolAction(ctx, "grep", input.Action, input, map[string]actionHandler[grepToolInput]{
		"text_search": func(ctx context.Context, input grepToolInput) (any, error) {
			root, roots, err := toolWorkspaceRoots(ctx)
			if err != nil {
				return nil, err
			}
			opts := search.TextSearchOptions{
				Root:          root,
				Roots:         roots,
				Path:          input.Path,
				Glob:          input.Glob,
				Query:         input.Query,
				Regex:         input.Regex,
				CaseSensitive: input.CaseSensitive,
				MaxResults:    limit,
				MaxFileBytes:  maxReadFileBytes,
			}
			matches, runErr = search.SearchText(ctx, opts)
			return nil, runErr
		},
		"ast_search": func(ctx context.Context, input grepToolInput) (any, error) {
			root, roots, err := toolWorkspaceRoots(ctx)
			if err != nil {
				return nil, err
			}
			matches, runErr = search.SearchAST(ctx, search.ASTSearchOptions{
				Root:         root,
				Roots:        roots,
				Path:         input.Path,
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

	filtered, total, truncated := search.FilterAndCapSearchMatches(matches, limit)
	h.attachFuncRanges(ctx, filtered)
	if len(filtered) == 0 {
		return grepResponse{
			Files:   map[string]grepFileRows{},
			Total:   0,
			Showing: 0,
			Message: emptyGrepMessage(false),
		}, nil
	}
	resp := buildGrepResponse(filtered, total, truncated)
	if message := grepMessage(false, resp.DroppedForPayload); message != "" {
		resp.Message = message
	}
	capGrepResponseBytes(&resp, middleware.ToolBudget("grep"))
	return resp, nil
}

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

func emptyGrepMessage(regexFallback bool) string {
	if regexFallback {
		return "regex parse failed; retried query as literal text; no matches found"
	}
	return "no matches found"
}

func (h handlerBase) attachFuncRanges(ctx context.Context, matches []search.SearchMatch) {
	if h.registry == nil || len(matches) == 0 {
		return
	}
	provider := newFuncRangeEnricher(ctx, h.registry)
	if provider == nil {
		return
	}
	lastRange := make(map[string][2]int)
	for index := range matches {
		start, end, _, ok := format.ResolveEnclosingFunctionRange(provider, fileURI(matches[index].AbsPath), matches[index].Line-1, lastRange)
		if !ok {
			continue
		}
		matches[index].FuncStart = start
		matches[index].FuncEnd = end
	}
}

func capGrepResponseBytes(resp *grepResponse, maxBytes int) {
	for {
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

func dropLastGrepRow(resp *grepResponse) bool {
	var maxFile string
	var maxRows int
	for file, fr := range resp.Files {
		if len(fr.Rows) > maxRows {
			maxRows = len(fr.Rows)
			maxFile = file
		}
	}
	if maxFile == "" {
		return false
	}
	fr := resp.Files[maxFile]
	if len(fr.Rows) <= 1 {
		delete(resp.Files, maxFile)
	} else {
		fr.Rows = fr.Rows[:len(fr.Rows)-1]
		resp.Files[maxFile] = fr
	}
	resp.Showing--
	if resp.Showing < 0 {
		resp.Showing = 0
	}
	return true
}

func buildGrepResponse(matches []search.SearchMatch, total int, truncated bool) grepResponse {
	files := make(map[string]grepFileRows, len(matches))
	hint := ""
	hasFuncRanges := false
	for _, match := range matches {
		row := []any{match.Line, match.Col, match.Text}
		if match.FuncStart > 0 && match.FuncEnd >= match.FuncStart {
			row = append(row, match.FuncStart, match.FuncEnd)
			hasFuncRanges = true
			hint = "step 2: use the returned func_start/func_end to read that function range, e.g. read_file(offset=func_start, limit=func_end-func_start+1)"
		}
		block := files[match.File]
		if len(block.Cols) == 0 {
			block.Cols = grepRowCols(hasFuncRanges)
		}
		block.Rows = append(block.Rows, row)
		files[match.File] = block
	}
	// Backfill cols on every file once we know whether func ranges
	// appeared anywhere in the result set, so the schema-declared
	// column layout matches actual row widths file-by-file.
	for path, block := range files {
		block.Cols = grepRowCols(hasFuncRanges)
		if hasFuncRanges {
			block.Rows = padGrepRows(block.Rows, len(block.Cols))
		}
		files[path] = block
	}
	return grepResponse{
		Files:     files,
		Total:     total,
		Showing:   len(matches),
		Truncated: truncated,
		Hint:      hint,
	}
}

// grepRowCols returns the column header that matches what buildGrepResponse
// actually writes into rows. Skipping func_start/func_end columns when no
// hit reports an enclosing function avoids advertising fields that aren't
// present in any row.
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

	// Sort file paths to have deterministic output order
	var files []string
	for f := range r.Files {
		files = append(files, f)
	}
	sort.Strings(files)

	for _, file := range files {
		fr := r.Files[file]
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

// numericRowValue tolerates both int (typical first call) and float64
// (post JSON round-trip) so plain-text rendering doesn't silently
// produce L0:0 if the response was demoted to map[string]any along
// the way (e.g. budget overflow re-marshal).
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
