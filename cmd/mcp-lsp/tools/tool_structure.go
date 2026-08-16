package tools

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// structureParams 是 structure 工具的入参，支持 file_path 和 language 写法。
type structureParams struct {
	Action     string `json:"action"`
	FilePath   string `json:"file_path"`
	LanguageID string `json:"language_id,omitempty"`
	Query      string `json:"query"`
	Language   string `json:"language"`
	MatchMode  string `json:"match_mode,omitempty"`
	MaxResults int    `json:"max_results"`
}

const (
	workspaceSymbolMatchExact = "exact"
	workspaceSymbolMatchFuzzy = "fuzzy"
)

// documentSymbolListResponse 是 document_symbol 的分页响应。
type documentSymbolListResponse struct {
	Data      []protocol.DocumentSymbol `json:"data"`
	Total     int                       `json:"total"`
	Showing   int                       `json:"showing"`
	Truncated bool                      `json:"truncated,omitempty"`
	Hint      string                    `json:"hint,omitempty"`
}

type workspaceSymbolListResponse struct {
	Data      []format.CompactWorkspaceSymbol
	Total     int
	Showing   int
	Truncated bool
	Hint      string
}

type foldingRangeListResponse struct {
	Data      []protocol.FoldingRange
	Total     int
	Showing   int
	Truncated bool
	Hint      string
}

// ToPlainText 把递归符号树扁平为稳定的前序 ROW。
func (response documentSymbolListResponse) ToPlainText() string {
	lines := []string{lineprotocol.HeaderLine(response.Total, response.Showing, response.Truncated, "symbol")}
	appendDocumentSymbolRows(&lines, response.Data, 0)
	return appendStructureHint(lines, response.Hint)
}

func appendDocumentSymbolRows(lines *[]string, symbols []protocol.DocumentSymbol, depth int) {
	for _, symbol := range symbols {
		*lines = append(*lines, lineprotocol.FieldsRecord("ROW",
			lineprotocol.Field{Key: "name", Value: symbol.Name},
			lineprotocol.Field{Key: "kind", Value: strconv.Itoa(int(symbol.Kind))},
			lineprotocol.Field{Key: "depth", Value: strconv.Itoa(depth)},
			lineprotocol.Field{Key: "start_line", Value: strconv.Itoa(format.FromLSP(symbol.Range.Start.Line))},
			lineprotocol.Field{Key: "start_col", Value: strconv.Itoa(format.FromLSP(symbol.Range.Start.Character))},
			lineprotocol.Field{Key: "end_line", Value: strconv.Itoa(format.FromLSP(symbol.Range.End.Line))},
			lineprotocol.Field{Key: "end_col", Value: strconv.Itoa(format.FromLSP(symbol.Range.End.Character))},
		))
		appendDocumentSymbolRows(lines, symbol.Children, depth+1)
	}
}

// ToPlainText 把 workspace symbol 列表渲染为紧凑 ROW。
func (response workspaceSymbolListResponse) ToPlainText() string {
	lines := []string{lineprotocol.HeaderLine(response.Total, response.Showing, response.Truncated, "symbol")}
	for _, symbol := range response.Data {
		lines = append(lines, lineprotocol.FieldsRecord("ROW",
			lineprotocol.Field{Key: "name", Value: symbol.Name},
			lineprotocol.Field{Key: "kind", Value: strconv.Itoa(symbol.Kind)},
			lineprotocol.Field{Key: "file", Value: symbol.File},
			lineprotocol.Field{Key: "line", Value: strconv.Itoa(symbol.Line)},
			lineprotocol.Field{Key: "col", Value: strconv.Itoa(symbol.Col)},
			lineprotocol.Field{Key: "container", Value: symbol.Container},
		))
	}
	return appendStructureHint(lines, response.Hint)
}

// ToPlainText 把 folding ranges 渲染为紧凑 ROW。
func (response foldingRangeListResponse) ToPlainText() string {
	lines := []string{lineprotocol.HeaderLine(response.Total, response.Showing, response.Truncated, "range")}
	for _, item := range response.Data {
		lines = append(lines, lineprotocol.FieldsRecord("ROW",
			lineprotocol.Field{Key: "start_line", Value: strconv.Itoa(format.FromLSP(item.StartLine))},
			lineprotocol.Field{Key: "end_line", Value: strconv.Itoa(format.FromLSP(item.EndLine))},
			lineprotocol.Field{Key: "kind", Value: item.Kind},
		))
	}
	return appendStructureHint(lines, response.Hint)
}

func appendStructureHint(lines []string, hint string) string {
	if hint = strings.TrimSpace(hint); hint != "" {
		lines = append(lines, lineprotocol.TextRecord("HINT", hint))
	}
	return strings.Join(lines, "\n")
}

// NewStructureHandler 创建 structure 工具处理器，按 action 延迟选择文件或语言级 manager。
func NewStructureHandler(registry lspmanager.Registry) ToolHandler {
	return newManagerTool("structure", middleware.TierSlow, registry, decodeLenient, func(ctx context.Context, registry lspmanager.Registry, req structureParams) (any, error) {
		// Resolve the manager lazily per action: workspace_symbol can use
		// the "language" parameter instead of "file_path", so we must not
		// call GetManagerForFile unconditionally.
		resolveManager := func() (lspmanager.Manager, error) {
			return managerForFile(ctx, registry, req.FilePath, req.LanguageID)
		}
		return dispatchToolAction(ctx, "structure", req.Action, req, map[string]actionHandler[structureParams]{
			"document_symbol": func(ctx context.Context, req structureParams) (any, error) {
				return runDocumentSymbolAction(ctx, req, resolveManager)
			},
			"workspace_symbol": func(ctx context.Context, req structureParams) (any, error) {
				mgr, languageID, err := resolveWorkspaceSymbolManager(ctx, registry, req.FilePath, req.Language, req.LanguageID)
				if err != nil {
					return nil, err
				}
				return runWorkspaceSymbols(ctx, mgr, languageID, req)
			},
			"folding_range": func(ctx context.Context, req structureParams) (any, error) {
				mgr, err := resolveManager()
				if err != nil {
					return nil, err
				}
				return runFoldingRanges(ctx, mgr, req)
			},
			"semantic_tokens": func(ctx context.Context, req structureParams) (any, error) {
				mgr, err := resolveManager()
				if err != nil {
					return nil, err
				}
				return runSemanticTokens(ctx, mgr, req)
			},
		})
	})
}

// runDocumentSymbolAction 分阶段记录 manager 解析与 LSP 请求耗时，定位冷启动阻塞。
func runDocumentSymbolAction(ctx context.Context, req structureParams, resolve func() (lspmanager.Manager, error)) (any, error) {
	log := pkglogger.Get()
	started := time.Now()
	log.InfoContext(ctx, "mcp-lsp structure stage started", "action", "document_symbol", "stage", "manager_resolution")
	mgr, err := resolve()
	if err != nil {
		return nil, err
	}
	log.InfoContext(ctx, "mcp-lsp structure stage completed", "action", "document_symbol", "stage", "manager_resolution", "duration_ms", time.Since(started).Milliseconds())
	started = time.Now()
	log.InfoContext(ctx, "mcp-lsp structure stage started", "action", "document_symbol", "stage", "lsp_request")
	result, err := runDocumentSymbols(ctx, mgr, req)
	log.InfoContext(ctx, "mcp-lsp structure stage completed", "action", "document_symbol", "stage", "lsp_request", "duration_ms", time.Since(started).Milliseconds())
	return result, err
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// resolveWorkspaceSymbolManager 根据 language 或 file_path 选择 workspace_symbol 的 manager。
// 两个定位方式必须二选一，避免目录路径被误当成源码文件启动语言服务。
func resolveWorkspaceSymbolManager(ctx context.Context, registry lspmanager.Registry, filePath, language, languageID string) (lspmanager.Manager, string, error) {
	language = normalizeWorkspaceSymbolLanguage(language)
	languageID = normalizeWorkspaceSymbolLanguage(languageID)
	filePath = strings.TrimSpace(filePath)
	if (filePath == "") == (language == "") {
		return nil, "", workspaceSymbolParamsError("exactly one of file_path or language is required")
	}
	if language != "" {
		return resolveWorkspaceLanguageManager(ctx, registry, language, languageID)
	}
	return resolveWorkspaceFileManager(ctx, registry, filePath, languageID)
}

func resolveWorkspaceLanguageManager(ctx context.Context, registry lspmanager.Registry, language, languageID string) (lspmanager.Manager, string, error) {
	if languageID != "" {
		return nil, "", workspaceSymbolParamsError("language_id is only valid with file_path; remove language_id")
	}
	if language == sqliteSQLLanguageID {
		return nil, "", workspaceSymbolParamsError("SQL workspace_symbol requires file_path so the SQLite sqlc owner can be validated")
	}
	if limitedDocumentFallbackLanguage(language) != "" {
		return nil, language, nil
	}
	manager, err := registry.GetManagerForLanguage(ctx, language)
	return manager, language, err
}

func resolveWorkspaceFileManager(ctx context.Context, registry lspmanager.Registry, filePath, languageID string) (lspmanager.Manager, string, error) {
	if err := validateWorkspaceSymbolFilePath(filePath); err != nil {
		return nil, "", workspaceSymbolParamsError(err.Error())
	}
	if languageID == "" {
		languageID = lspmanager.DetectLanguageID(workspaceSymbolPathForValidation(filePath))
	}
	if limitedDocumentFallbackLanguage(languageID) != "" {
		return nil, languageID, nil
	}
	manager, err := managerForFile(ctx, registry, filePath, languageID)
	if err != nil {
		if errors.Is(err, lspmanager.ErrUnsupportedLanguage) {
			return nil, "", errors.New("path must point to a source file with a configured language server; use language for workspace-wide search, and use file/grep for docs or config files")
		}
		return nil, "", err
	}
	return manager, languageID, nil
}

func workspaceSymbolParamsError(message string) error {
	return common.NewCodedToolError("invalid_params", errors.New(message), false,
		"choose-file_path-or-language; use-language_id-only-as-a-file_path-server-override")
}

// runDocumentSymbols 读取单文件符号树，并按 max_results 递归裁剪。
func runDocumentSymbols(
	ctx context.Context,
	manager lspmanager.Manager,
	req structureParams,
) (any, error) {
	filePath, err := resolveFilePath(ctx, req.FilePath)
	if err != nil {
		return nil, err
	}
	results, err := manager.DocumentSymbol(ctx, filePath)
	if err != nil {
		return nil, err
	}
	total := countDocumentSymbolNodes(results)
	limit := format.DocumentSymbolLimit(req.MaxResults)
	results = limitDocumentSymbols(results, limit)
	showing := countDocumentSymbolNodes(results)
	resp := documentSymbolListResponse{
		Data:      results,
		Total:     total,
		Showing:   showing,
		Truncated: showing < total,
	}
	if resp.Truncated {
		resp.Hint = "next: increase max_results or narrow the file/symbol scope"
	}
	return resp, nil
}

// runWorkspaceSymbols 直接委托 manager；capability 与 managed-document 同步必须由同一 owner 原子裁决。
func runWorkspaceSymbols(
	ctx context.Context,
	manager lspmanager.Manager,
	languageID string,
	req structureParams,
) (any, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, errors.New("query is required")
	}
	matchMode, err := normalizeWorkspaceSymbolMatchMode(req.MatchMode)
	if err != nil {
		return nil, err
	}
	if limitedDocumentFallbackLanguage(languageID) != "" {
		return unsupportedCapabilityEmptyResult("workspace symbol", languageID), nil
	}
	if manager == nil {
		return nil, errManagerUnavailable
	}
	limit := format.WorkspaceSymbolLimit(req.MaxResults, format.VerbosityCompact)
	results, err := manager.WorkspaceSymbol(ctx, query, languageID)
	if isUnsupportedCapability(err) {
		return unsupportedCapabilityEmptyResult("workspace symbol", languageID), nil
	}
	if err != nil {
		return nil, err
	}
	items := format.CompactWorkspaceSymbols(results)
	if strings.TrimSpace(req.FilePath) != "" {
		items, err = filterWorkspaceSymbolsForFile(ctx, items, req.FilePath)
		if err != nil {
			return nil, err
		}
	}
	items = selectWorkspaceSymbolMatches(items, query, matchMode)
	total := len(items)
	items = limitSlice(items, limit)
	response := workspaceSymbolListResponse{
		Data: items, Total: total, Showing: len(items), Truncated: len(items) < total,
		Hint: "next: increase max_results or narrow query/language/file_path",
	}
	if matchMode == workspaceSymbolMatchExact {
		response.Hint = "next: retry with match_mode=fuzzy to include non-exact symbol names"
	}
	return response, nil
}

// normalizeWorkspaceSymbolMatchMode 收敛 workspace_symbol 匹配模式，未知值立即失败。
func normalizeWorkspaceSymbolMatchMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", workspaceSymbolMatchExact:
		return workspaceSymbolMatchExact, nil
	case workspaceSymbolMatchFuzzy:
		return workspaceSymbolMatchFuzzy, nil
	default:
		return "", errors.New("match_mode must be exact or fuzzy")
	}
}

// filterWorkspaceSymbolsForFile 把 file_path 语义落实为结果文件过滤，而不只是 manager 选择器。
func filterWorkspaceSymbolsForFile(ctx context.Context, items []format.CompactWorkspaceSymbol, filePath string) ([]format.CompactWorkspaceSymbol, error) {
	target, err := toolResolvePath(ctx, workspaceSymbolPathForValidation(filePath))
	if err != nil {
		return nil, err
	}
	filtered := make([]format.CompactWorkspaceSymbol, 0, len(items))
	for _, item := range items {
		candidate, err := toolResolvePath(ctx, item.File)
		if err == nil && candidate.AbsPath == target.AbsPath {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

// selectWorkspaceSymbolMatches 默认只返回精确名称；fuzzy 模式把精确项稳定排在前面。
func selectWorkspaceSymbolMatches(items []format.CompactWorkspaceSymbol, query, matchMode string) []format.CompactWorkspaceSymbol {
	exact := make([]format.CompactWorkspaceSymbol, 0, len(items))
	fuzzy := make([]format.CompactWorkspaceSymbol, 0, len(items))
	for _, item := range items {
		if item.Name == query {
			exact = append(exact, item)
			continue
		}
		fuzzy = append(fuzzy, item)
	}
	if matchMode == workspaceSymbolMatchExact {
		return exact
	}
	return append(exact, fuzzy...)
}

// runFoldingRanges 返回文件折叠范围，供调用方理解大文件结构。
func runFoldingRanges(
	ctx context.Context,
	manager lspmanager.Manager,
	req structureParams,
) (any, error) {
	filePath, err := resolveFilePath(ctx, req.FilePath)
	if err != nil {
		return nil, err
	}
	results, err := manager.FoldingRange(ctx, filePath)
	if err != nil {
		return nil, err
	}
	total := len(results)
	items := limitSlice(results, shared.ClampLimit(req.MaxResults, 1, protocol.XRefResultLimit, protocol.XRefResultLimit))
	response := foldingRangeListResponse{
		Data: items, Total: total, Showing: len(items), Truncated: len(items) < total,
	}
	if response.Truncated {
		response.Hint = "next: increase max_results or narrow file scope"
	}
	return response, nil
}

// runSemanticTokens 返回语义令牌，并按协议上限裁剪。
func runSemanticTokens(
	ctx context.Context,
	manager lspmanager.Manager,
	req structureParams,
) (any, error) {
	filePath, err := resolveFilePath(ctx, req.FilePath)
	if err != nil {
		return nil, err
	}
	result, err := manager.SemanticTokens(ctx, filePath)
	if err != nil {
		return nil, err
	}
	limit := protocol.SemanticTokenResultLimit
	if req.MaxResults > 0 && req.MaxResults < limit {
		limit = req.MaxResults
	}
	result = capSemanticTokens(result, limit)
	if result == nil || (len(result.Data) == 0 && len(result.Decoded) == 0) {
		return emptyListEnvelope{
			Success: true,
			Data:    []any{},
			Meta:    resultMeta{Count: 0, Message: "no semantic tokens found"},
		}, nil
	}
	return format.NormalizeForDisplay(result), nil
}

// validateWorkspaceSymbolFilePath 拒绝目录路径，避免 workspace_symbol 扫描语义不明确。
func validateWorkspaceSymbolFilePath(filePath string) error {
	filePath = strings.TrimSpace(filePath)
	if stat, err := os.Stat(workspaceSymbolPathForValidation(filePath)); err == nil && stat.IsDir() {
		return errors.New("directory path is not supported for workspace_symbol; use language instead")
	}
	return nil
}

// normalizeWorkspaceSymbolLanguage 标准化 language 参数。
func normalizeWorkspaceSymbolLanguage(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// workspaceSymbolPathForValidation 把 file:// 路径转换为本地路径用于 stat 校验。
func workspaceSymbolPathForValidation(path string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "file://") {
		if absPath, err := format.AbsolutePathFromURI(path); err == nil {
			return absPath
		}
	}
	return path
}

// limitDocumentSymbols 按节点数量上限裁剪 document symbol 树。
func limitDocumentSymbols(symbols []protocol.DocumentSymbol, limit int) []protocol.DocumentSymbol {
	if len(symbols) == 0 || limit <= 0 {
		return nil
	}
	remaining := limit
	return limitDocumentSymbolNodes(symbols, &remaining)
}

// limitDocumentSymbolNodes 递归裁剪 symbol 树，并共享 remaining 计数。
func limitDocumentSymbolNodes(
	symbols []protocol.DocumentSymbol,
	remaining *int,
) []protocol.DocumentSymbol {
	if len(symbols) == 0 || remaining == nil || *remaining <= 0 {
		return nil
	}
	capped := make([]protocol.DocumentSymbol, 0, len(symbols))
	for i := range symbols {
		if *remaining <= 0 {
			break
		}
		*remaining--
		item := symbols[i]
		item.Children = limitDocumentSymbolNodes(item.Children, remaining)
		capped = append(capped, item)
	}
	return capped
}

// countDocumentSymbolNodes 统计 symbol 树中节点总数。
func countDocumentSymbolNodes(symbols []protocol.DocumentSymbol) int {
	total := 0
	for i := range symbols {
		total++
		total += countDocumentSymbolNodes(symbols[i].Children)
	}
	return total
}

func capSemanticTokens(
	result *protocol.SemanticTokensResult,
	limit int,
) *protocol.SemanticTokensResult {
	if result == nil {
		return nil
	}
	if limit <= 0 {
		limit = protocol.SemanticTokenResultLimit
	}
	out := *result
	if len(result.Decoded) > 0 {
		out.Decoded = limitSlice(result.Decoded, limit)
	}
	tokenLimit := limit
	if len(out.Decoded) > 0 {
		tokenLimit = len(out.Decoded)
	}
	out.Data = limitSemanticTokenData(result.Data, tokenLimit)
	return &out
}

func limitSemanticTokenData(data []int, tokenLimit int) []int {
	if len(data) == 0 {
		return nil
	}
	if tokenLimit <= 0 {
		tokenLimit = protocol.SemanticTokenResultLimit
	}
	maxData := tokenLimit * 5
	if len(data) > maxData {
		data = data[:maxData]
	}
	return append([]int(nil), data...)
}
