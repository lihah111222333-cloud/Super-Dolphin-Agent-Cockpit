package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/search"
	common "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/runtime"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type ToolHandler = middleware.Handler

type Handler = ToolHandler

type decodeMode int

type actionHandler[T any] func(context.Context, T) (any, error)

const (
	decodeRaw decodeMode = iota
	decodeLenient
	decodeStrict
)

func toolWorkspaceRoot(ctx context.Context) (string, error) {
	return common.WorkspaceRootFromContextStrict(ctx)
}

func toolWorkspaceRoots(ctx context.Context) (string, []string, error) {
	roots, err := common.WorkspaceRootsFromContextStrict(ctx)
	if err != nil {
		return "", nil, err
	}
	if len(roots) == 0 {
		return "", nil, common.ErrMissingWorkspaceRoots
	}
	return roots[0], append([]string(nil), roots[1:]...), nil
}

func toolResolvePath(ctx context.Context, target string) (search.PathInfo, error) {
	root, roots, err := toolWorkspaceRoots(ctx)
	if err != nil {
		return search.PathInfo{}, err
	}
	return search.ResolvePathInRoots(root, roots, target)
}

func newManagerTool[T any](
	name string,
	tier time.Duration,
	registry lspmanager.Registry,
	mode decodeMode,
	dispatch func(context.Context, lspmanager.Registry, T) (any, error),
) ToolHandler {
	if registry == nil {
		return missingManagerHandler()
	}
	return wrapToolHandler(name, tier, func(ctx context.Context, params json.RawMessage) (any, error) {
		req, err := decodeToolParams[T](params, mode)
		if err != nil {
			return nil, err
		}
		return dispatch(ctx, registry, req)
	})
}

func decodeToolParams[T any](raw json.RawMessage, mode decodeMode) (T, error) {
	var value T
	var err error
	switch mode {
	case decodeLenient:
		err = decodeLenientToolParams(raw, &value)
	case decodeStrict:
		err = decodeStrictToolParams(raw, &value)
	default:
		err = decodeRawToolParams(raw, &value)
	}
	if err != nil {
		return value, err
	}
	return value, nil
}

func decodeRawToolParams[T any](raw json.RawMessage, value *T) error {
	return unmarshalToolParams(raw, value)
}

func decodeLenientToolParams[T any](raw json.RawMessage, value *T) error {
	return unmarshalToolParams(normalizeOptionalToolParams(raw), value)
}

func decodeStrictToolParams[T any](raw json.RawMessage, value *T) error {
	decoder := json.NewDecoder(bytes.NewReader(normalizeOptionalToolParams(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return formatDecodeParamsError(err)
	}
	var extra struct{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("decode params: unexpected trailing JSON payload")
	}
	return nil
}

func normalizeOptionalToolParams(raw json.RawMessage) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return []byte("{}")
	}
	return trimmed
}

func unmarshalToolParams[T any](raw []byte, value *T) error {
	if err := json.Unmarshal(raw, value); err != nil {
		return formatDecodeParamsError(err)
	}
	return nil
}

func formatDecodeParamsError(err error) error {
	hint := "next: pass numeric fields as JSON numbers, string fields as JSON strings, and remove unknown fields"
	if migration := legacyPositionMigrationHint(err); migration != "" {
		hint = hint + "; " + migration
	}
	return fmt.Errorf("decode params: %w; %s", err, hint)
}

// legacyPositionMigrationHint detects the old {file_path, line, column}
// fields in a strict-decode error and returns a migration hint pointing
// callers at the unified pos parameter. It returns "" when the error is
// unrelated to those fields so callers fall back to the generic hint.
func legacyPositionMigrationHint(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, `"file_path"`),
		strings.Contains(msg, `"line"`),
		strings.Contains(msg, `"column"`),
		strings.Contains(msg, "field file_path"),
		strings.Contains(msg, "field line"),
		strings.Contains(msg, "field column"):
		return `the inspect/xref/completion tools merged file_path/line/column into a single pos parameter formatted as "file_path:line:column" (example internal/foo.go:42:9)`
	default:
		return ""
	}
}

func dispatchToolAction[T any](
	ctx context.Context,
	label string,
	action string,
	req T,
	handlers map[string]actionHandler[T],
) (any, error) {
	normalized := normalizeAction(action)
	if alias := legacyActionAlias(label, normalized); alias != "" {
		normalized = alias
	}
	handler, ok := handlers[normalized]
	if !ok {
		return nil, unsupportedActionError(label, action, handlers)
	}
	return handler(ctx, req)
}

func unsupportedActionError[T any](label string, action string, handlers map[string]actionHandler[T]) error {
	valid := validActionNames(handlers)
	message := fmt.Sprintf("unsupported %s action %q (valid actions: %s)", label, action, strings.Join(valid, ", "))
	if closest := closestAction(normalizeAction(action), valid); closest != "" {
		message += fmt.Sprintf("; did you mean %q?", closest)
	}
	if hint := legacyActionHint(label, normalizeAction(action)); hint != "" {
		message += "; " + hint
	}
	return errors.New(message)
}

func validActionNames[T any](handlers map[string]actionHandler[T]) []string {
	names := make([]string, 0, len(handlers))
	for name := range handlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func legacyActionAlias(label string, action string) string {
	switch label {
	case "file":
		switch action {
		case "read":
			return "read_file"
		case "open":
			return "open_file"
		}
	}
	return ""
}

// legacyActionHint 处理legacy动作hint。
func legacyActionHint(label string, action string) string {
	switch label {
	case "file":
		switch action {
		case "read":
			return `legacy action "read" is accepted as "read_file"`
		case "open":
			return `legacy action "open" is accepted as "open_file"`
		}
	case "xref":
		if action == "references" {
			return `use tool "xref" with action "references"`
		}
	}
	return ""
}

func closestAction(action string, valid []string) string {
	if action == "" {
		return ""
	}
	best := ""
	bestDistance := 3
	for _, candidate := range valid {
		distance := editDistance(action, candidate)
		if distance < bestDistance {
			bestDistance = distance
			best = candidate
		}
	}
	return best
}

// editDistance 处理编辑distance。
func editDistance(a string, b string) int {
	ar := []rune(a)
	br := []rune(b)
	prev := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur := make([]int, len(br)+1)
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 0
			if ar[i-1] != br[j-1] {
				cost = 1
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(br)]
}

func missingDependencyHandler(message string) ToolHandler {
	return func(context.Context, json.RawMessage) (any, error) {
		return nil, errors.New(message)
	}
}

func missingManagerHandler() ToolHandler {
	return missingDependencyHandler("lsp manager is not available; use text_search or read_file as alternatives")
}

func managerForFile(ctx context.Context, registry lspmanager.Registry, filePath string, languageID string) (lspmanager.Manager, error) {
	if registry == nil {
		return nil, errManagerUnavailable
	}
	return registry.GetManagerForFileWithLanguage(ctx, filePath, normalizeLanguageIDOverride(languageID))
}

func normalizeLanguageIDOverride(languageID string) string {
	return strings.ToLower(strings.TrimSpace(languageID))
}

func requireFilePath(raw string) (string, error) {
	filePath := strings.TrimSpace(raw)
	if filePath == "" {
		return "", errors.New("file_path is required; pass an absolute or workspace-relative path")
	}
	return filePath, nil
}

func requirePosition(line, column int) (protocol.Position, error) {
	if line <= 0 {
		return protocol.Position{}, errors.New("line must be >= 1")
	}
	if column <= 0 {
		return protocol.Position{}, errors.New("column must be >= 1")
	}
	return protocol.Position{
		Line:      line - 1,
		Character: column - 1,
	}, nil
}

func resolveFilePositionRequest(ctx context.Context, params filePositionParams) (string, protocol.Position, error) {
	filePathRaw, line, col, err := parsePos(params.Pos)
	if err != nil {
		return "", protocol.Position{}, err
	}
	filePath, err := resolveFilePath(ctx, filePathRaw)
	if err != nil {
		return "", protocol.Position{}, err
	}
	position, err := requirePosition(line, col)
	if err != nil {
		return "", protocol.Position{}, err
	}
	if err := validateResolvedFilePosition(filePath, line, col); err != nil {
		return "", protocol.Position{}, err
	}
	return filePath, position, nil
}

func parsePos(pos string) (string, int, int, error) {
	filePath, line, col, hasCol, err := parseFilePos(pos, true)
	if err != nil {
		return "", 0, 0, err
	}
	if !hasCol {
		return "", 0, 0, fmt.Errorf("invalid pos format %q; expected 'file_path:line:column' (example internal/foo.go:42:9)", pos)
	}
	return filePath, line, col, nil
}

// parseFilePos accepts either "file:line" or "file:line:col". When
// requireCol is true the caller demands the three-segment form. The
// returned hasCol reports whether the caller passed the column segment.
//
// Centralising the parser lets the file tool reuse the same syntax (with
// only file:line) that inspect/xref/completion already accept, so the
// model can copy positions across tools without adapting the format.
// parseFilePos 解析文件pos。
func parseFilePos(pos string, requireCol bool) (string, int, int, bool, error) {
	pos = strings.TrimSpace(pos)
	if pos == "" {
		return "", 0, 0, false, errors.New("position parameter 'pos' is empty; expected 'file_path:line[:column]' (example internal/foo.go:42:9)")
	}
	lastColon := strings.LastIndex(pos, ":")
	if lastColon == -1 {
		return "", 0, 0, false, fmt.Errorf("invalid pos format %q; expected 'file_path:line[:column]' (example internal/foo.go:42:9)", pos)
	}
	tailStr := pos[lastColon+1:]
	tail, ok := parsePositivePosSegment(tailStr)
	if !ok {
		return "", 0, 0, false, fmt.Errorf("invalid trailing segment %q in pos %q; expected a positive integer line or column (format 'file_path:line[:column]')", tailStr, pos)
	}
	remaining := pos[:lastColon]
	secondLastColon := strings.LastIndex(remaining, ":")
	if secondLastColon == -1 {
		return parseFileLinePos(pos, remaining, tail, requireCol)
	}
	maybeLineStr := remaining[secondLastColon+1:]
	maybeLine, ok := parsePositivePosSegment(maybeLineStr)
	if !ok {
		// remaining[secondLastColon+1:] is not a number → treat
		// the second colon as part of the path (e.g. Windows
		// drive paths) and parse "file:line".
		return parseFileLinePos(pos, remaining, tail, requireCol)
	}
	return parseFileLineColumnPos(pos, remaining[:secondLastColon], maybeLine, tail)
}

func parsePositivePosSegment(value string) (int, bool) {
	parsed, parseErr := strconv.Atoi(value)
	return parsed, parseErr == nil && parsed > 0
}

func parseFileLinePos(pos string, rawFilePath string, line int, requireCol bool) (string, int, int, bool, error) {
	filePath := strings.TrimSpace(rawFilePath)
	if filePath == "" {
		return "", 0, 0, false, fmt.Errorf("invalid pos format %q; missing file path before ':line' (example internal/foo.go:42)", pos)
	}
	if requireCol {
		return "", 0, 0, false, fmt.Errorf("invalid pos format %q; expected 'file_path:line:column' (example internal/foo.go:42:9)", pos)
	}
	return filePath, line, 0, false, nil
}

func parseFileLineColumnPos(pos string, rawFilePath string, line int, col int) (string, int, int, bool, error) {
	filePath := strings.TrimSpace(rawFilePath)
	if filePath == "" {
		return "", 0, 0, false, fmt.Errorf("invalid pos format %q; missing file path before ':line:column' (example internal/foo.go:42:9)", pos)
	}
	return filePath, line, col, true, nil
}

func validateResolvedFilePosition(filePath string, line int, column int) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	lines := splitNormalizedLines(string(content))
	if line > len(lines) {
		return newLineOutOfRangeError(line, len(lines))
	}
	lineText := lines[line-1]
	lineLength := len([]rune(lineText))
	maxColumn := lineLength + 1
	if column > maxColumn {
		return newPositionOutOfRangeError(line, column, lineText, lineLength, maxColumn)
	}
	return nil
}

func newLineOutOfRangeError(line int, lineCount int) error {
	err := common.NewCodedToolError(
		"line_out_of_range",
		fmt.Errorf("line %d is beyond end of file with %d lines", line, lineCount),
		false,
		"next: file action=read_file pos=<file>:1 limit=200, then retry with an existing 1-based line in pos=<file>:<line>:<col>",
	)
	var coded *common.CodedToolError
	if errors.As(err, &coded) {
		coded.Meta = map[string]any{
			"requested_line": line,
			"line_count":     lineCount,
		}
	}
	return err
}

var positionIdentifierRE = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

func newPositionOutOfRangeError(line int, column int, lineText string, lineLength int, maxColumn int) error {
	err := common.NewCodedToolError(
		"position_out_of_range",
		fmt.Errorf("column %d is beyond end of line %d, max column is %d", column, line, maxColumn),
		false,
		"next: retry with pos=<file>:<line>:<col> using a column inside the target identifier or at end of line; inspect meta.line_text and meta.suggested_columns",
	)
	var coded *common.CodedToolError
	if errors.As(err, &coded) {
		coded.Meta = map[string]any{
			"line":              line,
			"line_text":         lineText,
			"line_length":       lineLength,
			"max_column":        maxColumn,
			"requested_column":  column,
			"suggested_columns": suggestedIdentifierColumns(lineText),
		}
	}
	return err
}

func suggestedIdentifierColumns(lineText string) []map[string]any {
	matches := positionIdentifierRE.FindAllStringIndex(lineText, -1)
	suggestions := make([]map[string]any, 0, len(matches))
	for _, match := range matches {
		identifier := lineText[match[0]:match[1]]
		suggestions = append(suggestions, map[string]any{
			"identifier": identifier,
			"column":     match[0] + 1,
		})
	}
	return suggestions
}

func normalizeAction(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func renderListResult[T any](items []T, limit int, emptyMessage string, render func([]T, int) any) (any, error) {
	total := len(items)
	items = limitSlice(items, limit)
	if len(items) == 0 {
		return emptyListEnvelope{
			Success: true,
			Data:    []any{},
			Meta:    resultMeta{Count: 0, Message: emptyMessage},
		}, nil
	}
	return render(items, total), nil
}

func wrapToolHandler(toolName string, tier time.Duration, handler middleware.Handler) middleware.Handler {
	log := pkglogger.Get()
	scopedHandler := func(ctx context.Context, params json.RawMessage) (any, error) {
		var err error
		ctx, err = contextWithExplicitToolWorkDir(ctx, params)
		if err != nil {
			return nil, err
		}
		return handler(ctx, params)
	}
	chained := middleware.Chain(
		scopedHandler,
		middleware.Recovery(log, toolName),
		middleware.Logging(log, toolName),
		middleware.Timeout(tier),
	)
	return middleware.WithOutputBudget(toolName, chained, middleware.Budget{})
}

type explicitToolWorkDirParams struct {
	WorkDir string `json:"work_dir,omitempty"`
}

// contextWithExplicitToolWorkDir 处理带explicit工具work目录的上下文。
func contextWithExplicitToolWorkDir(ctx context.Context, params json.RawMessage) (context.Context, error) {
	trimmed := bytes.TrimSpace(params)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ctx, nil
	}
	var input explicitToolWorkDirParams
	if err := json.Unmarshal(trimmed, &input); err != nil {
		return ctx, fmt.Errorf("parse explicit tool work_dir params: %w", err)
	}
	workDir := strings.TrimSpace(input.WorkDir)
	if workDir == "" {
		return ctx, nil
	}
	scopedCtx, _, err := contextWithExplicitWorkDir(ctx, workDir)
	if err != nil {
		return ctx, err
	}
	return scopedCtx, nil
}

func contextWithExplicitWorkDir(ctx context.Context, workDir string) (context.Context, string, error) {
	normalized, err := normalizeExplicitWorkDir(ctx, workDir)
	if err != nil {
		return ctx, "", err
	}
	scope, _ := common.ToolScopeFromContext(ctx)
	scope.CWD = normalized
	scope.WorkspaceRoots = append(scope.WorkspaceRoots, normalized)
	if strings.TrimSpace(scope.Family) == "" {
		scope.Family = "lsp"
	}
	return common.WithToolScope(ctx, scope), normalized, nil
}

// normalizeExplicitWorkDir 规范化explicitwork目录。
func normalizeExplicitWorkDir(ctx context.Context, workDir string) (string, error) {
	trimmed := strings.TrimSpace(workDir)
	if trimmed == "" {
		return "", errors.New("work_dir is required")
	}
	if !filepath.IsAbs(trimmed) {
		root, err := common.WorkspaceRootFromContextStrict(ctx)
		if err != nil {
			return "", fmt.Errorf("relative work_dir requires trusted workspace root: %w", err)
		}
		trimmed = filepath.Join(root, trimmed)
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve work_dir: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(absolute))
	if err != nil {
		return "", fmt.Errorf("resolve work_dir: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat work_dir: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("work_dir is not a directory: %s", resolved)
	}
	return filepath.Clean(resolved), nil
}

// funcRangeEnricher fetches DocumentSymbols on demand so location-emitting
// tools (inspect.definition, xref.references, ...) can attach
// FuncStart/FuncEnd to each LocationResult. The cache keeps repeated
// lookups for the same file cheap when a request returns multiple
// locations clustered in one source file.
type funcRangeEnricher struct {
	ctx      context.Context
	registry lspmanager.Registry
	cache    map[string][]protocol.DocumentSymbol
}

func newFuncRangeEnricher(ctx context.Context, registry lspmanager.Registry) *funcRangeEnricher {
	if registry == nil {
		return nil
	}
	return &funcRangeEnricher{
		ctx:      ctx,
		registry: registry,
		cache:    make(map[string][]protocol.DocumentSymbol),
	}
}

// Symbols 处理符号。
func (p *funcRangeEnricher) Symbols(absPath string) ([]protocol.DocumentSymbol, error) {
	if p == nil {
		return nil, errors.New("funcRangeEnricher is nil")
	}
	if cached, ok := p.cache[absPath]; ok {
		return cached, nil
	}
	mgr, err := p.registry.GetManagerForFile(p.ctx, absPath)
	if err != nil {
		return nil, err
	}
	symbols, err := mgr.DocumentSymbol(p.ctx, fileURI(absPath))
	if err != nil {
		return nil, err
	}
	p.cache[absPath] = symbols
	return symbols, nil
}

// ToPlainText 渲染为纯文本。
func (e emptyListEnvelope) ToPlainText() string {
	if e.Meta.Message != "" {
		return e.Meta.Message
	}
	return "No items found."
}
