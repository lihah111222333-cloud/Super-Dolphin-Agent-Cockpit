package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	common "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/runtime"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/search"
)

const (
	defaultSearchResults = 50
	maxSearchResults     = 50
	grepTruncatedHint    = "next: adjust max_results, narrow path/glob, refine query, or search a specific file"
	grepFuncRangeHint    = "next: file action=read_file pos=<file>:<func_start> limit=<func_end-func_start+1>"
)

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

// UnmarshalJSON 解码JSON。
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

type grepPathInput struct {
	name string
	raw  json.RawMessage
}

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

type grepFileRows struct {
	Cols []string `json:"cols"`
	Rows [][]any  `json:"rows"`
}

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

// NewGrepHandler 创建grep处理器。
func NewGrepHandler(cfg Config) Handler {
	handler := handlerBase{
		root:     resolveRoot(cfg.WorkspaceRoot),
		registry: cfg.Registry,
	}
	return Handler(wrapToolHandler("grep", middleware.TierSlow, handler.handleGrep))
}

// handleGrep 处理grep。
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
			root, roots, err := toolWorkspaceRoots(ctx)
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

func searchSiblingWorkspaceOnRuntimeFallback(ctx context.Context, opts search.TextSearchOptions) ([]search.SearchMatch, error) {
	relPath, root, parent, ok := runtimeSiblingSearchScope(ctx, opts)
	if !ok {
		return nil, nil
	}
	configured := configuredWorkspaceRoots(root, opts.Roots)
	candidates, err := collectSiblingWorkspaceFileCandidates(parent, relPath, configured)
	if err != nil {
		return nil, err
	}
	claudeWorktreeCandidates, err := collectClaudeWorktreeFileCandidates(root, relPath, configured)
	if err != nil {
		return nil, err
	}
	candidates = appendUniqueWorkspaceCandidates(candidates, claudeWorktreeCandidates...)
	return searchUniqueSiblingWorkspaceText(ctx, opts, root, relPath, candidates)
}

// runtimeSiblingSearchScope 处理运行时siblingsearch作用域。
func runtimeSiblingSearchScope(ctx context.Context, opts search.TextSearchOptions) (string, string, string, bool) {
	if !common.RuntimeWorkspaceScopeFallbackFromContext(ctx) {
		return "", "", "", false
	}
	if len(opts.Paths) > 0 {
		return "", "", "", false
	}
	relPath := strings.TrimSpace(opts.Path)
	if relPath == "" || filepath.IsAbs(relPath) {
		return "", "", "", false
	}
	root := filepath.Clean(opts.Root)
	parent := filepath.Dir(root)
	if parent == "" || parent == "." || parent == root {
		return "", "", "", false
	}
	return relPath, root, parent, true
}

func configuredWorkspaceRoots(root string, additionalRoots []string) map[string]struct{} {
	configured := map[string]struct{}{filepath.Clean(root): {}}
	for _, additional := range additionalRoots {
		configured[filepath.Clean(additional)] = struct{}{}
	}
	return configured
}

func collectSiblingWorkspaceFileCandidates(parent, relPath string, configured map[string]struct{}) ([]string, error) {
	return collectWorkspaceFileCandidates(parent, relPath, configured, "runtime fallback sibling search path")
}

func collectClaudeWorktreeFileCandidates(root, relPath string, configured map[string]struct{}) ([]string, error) {
	worktreesDir := filepath.Join(root, ".claude", "worktrees")
	info, err := os.Stat(worktreesDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat runtime fallback Claude worktrees %s: %w", worktreesDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("runtime fallback Claude worktrees path is not a directory: %s", worktreesDir)
	}
	return collectWorkspaceFileCandidates(worktreesDir, relPath, configured, "runtime fallback Claude worktree search path")
}

// collectWorkspaceFileCandidates 收集工作区文件候选项。
func collectWorkspaceFileCandidates(parent, relPath string, configured map[string]struct{}, errorPrefix string) ([]string, error) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil, fmt.Errorf("read %s parent %s: %w", errorPrefix, parent, err)
	}
	candidates := make([]string, 0, 1)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidateRoot := filepath.Join(parent, entry.Name())
		if _, ok := configured[filepath.Clean(candidateRoot)]; ok {
			continue
		}
		candidateTarget := filepath.Join(candidateRoot, relPath)
		info, err := os.Lstat(candidateTarget)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("stat %s %s: %w", errorPrefix, candidateTarget, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s %q cannot be a symlink", errorPrefix, candidateTarget)
		}
		if info.IsDir() {
			continue
		}
		candidates = append(candidates, candidateRoot)
	}
	sort.Strings(candidates)
	return candidates, nil
}

func appendUniqueWorkspaceCandidates(candidates []string, extra ...string) []string {
	if len(extra) == 0 {
		return candidates
	}
	seen := make(map[string]struct{}, len(candidates)+len(extra))
	for _, candidate := range candidates {
		seen[filepath.Clean(candidate)] = struct{}{}
	}
	for _, candidate := range extra {
		cleaned := filepath.Clean(candidate)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		candidates = append(candidates, candidate)
	}
	sort.Strings(candidates)
	return candidates
}

// searchUniqueSiblingWorkspaceText 搜索uniquesibling工作区文本。
func searchUniqueSiblingWorkspaceText(ctx context.Context, opts search.TextSearchOptions, root, relPath string, candidates []string) ([]search.SearchMatch, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	matchedRoots := make([]string, 0, 1)
	var matched []search.SearchMatch
	for _, candidate := range candidates {
		candidateOpts := opts
		candidateOpts.Root = candidate
		candidateOpts.Roots = nil
		matches, err := search.SearchText(ctx, candidateOpts)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			continue
		}
		matchedRoots = append(matchedRoots, candidate)
		matched = matches
	}
	if len(matchedRoots) > 1 {
		return nil, fmt.Errorf("runtime fallback workspace root %s is stale for relative search path %q: multiple sibling workspaces matched query [%s]; pass work_dir or trusted _cwd/_workspaceRoots", root, relPath, strings.Join(matchedRoots, ", "))
	}
	return matched, nil
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

// dropLastGrepRow 去掉lastgreprow。
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

// buildGrepResponse 构建grep响应。
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
	// Backfill cols on every file once we know whether func ranges
	// appeared anywhere in the result set, so the schema-declared
	// column layout matches actual row widths file-by-file.
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

// ToPlainText 渲染为纯文本。
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
