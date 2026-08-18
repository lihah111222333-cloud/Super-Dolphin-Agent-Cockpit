package tools

import (
	"context"
	"errors"
	"fmt"
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

// structureParams 是 structure 工具的入参，支持 file_path 和 workspace_language 定位方式。
type structureParams struct {
	Action            string `json:"action"`
	FilePath          string `json:"file_path"`
	LanguageID        string `json:"language_id,omitempty"`
	Query             string `json:"query"`
	WorkspaceLanguage string `json:"workspace_language,omitempty"`
	MatchMode         string `json:"match_mode,omitempty"`
	MaxResults        int    `json:"max_results"`
}

const (
	workspaceSymbolMatchExact = "exact"
	workspaceSymbolMatchFuzzy = "fuzzy"
)

// documentSymbolListResponse 是 document_symbol 的分页响应。
type documentSymbolListResponse struct {
	Data           []protocol.DocumentSymbol `json:"data"`
	Total          int                       `json:"total"`
	Showing        int                       `json:"showing"`
	Truncated      bool                      `json:"truncated,omitempty"`
	FailureReason  string                    `json:"failure_reason,omitempty"`
	EffectiveLimit int                       `json:"effective_limit,omitempty"`
	NextStep       string                    `json:"next_step,omitempty"`
	Hint           string                    `json:"hint,omitempty"`
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

type semanticTokenListResponse struct {
	Legend    protocol.SemanticTokensLegend
	Data      []protocol.DecodedSemanticToken
	Total     int
	Truncated bool
	Hint      string
}

// ToPlainText 把递归符号树扁平为稳定的前序 ROW。
func (response documentSymbolListResponse) ToPlainText() string {
	lines := []string{lineprotocol.HeaderLine(response.Total, response.Showing, response.Truncated, "symbol")}
	if response.FailureReason != "" {
		lines = append(lines, lineprotocol.FieldsRecord("ATTR",
			lineprotocol.Field{Key: "failure_reason", Value: response.FailureReason},
			lineprotocol.Field{Key: "effective_limit", Value: strconv.Itoa(response.EffectiveLimit)},
			lineprotocol.Field{Key: "next_step", Value: response.NextStep},
		))
	}
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

// ToPlainText 输出 initialize legend 和已解码 token，不重复原始整数数组。
func (response semanticTokenListResponse) ToPlainText() string {
	lines := []string{lineprotocol.HeaderLine(response.Total, len(response.Data), response.Truncated, "token")}
	lines = append(lines, lineprotocol.FieldsRecord("LEGEND",
		lineprotocol.Field{Key: "token_types", Value: strings.Join(response.Legend.TokenTypes, ",")},
		lineprotocol.Field{Key: "token_modifiers", Value: strings.Join(response.Legend.TokenModifiers, ",")},
	))
	for _, token := range response.Data {
		lines = append(lines, lineprotocol.FieldsRecord("ROW",
			lineprotocol.Field{Key: "line", Value: strconv.Itoa(token.Line)},
			lineprotocol.Field{Key: "col", Value: strconv.Itoa(token.StartCharacter)},
			lineprotocol.Field{Key: "length", Value: strconv.Itoa(token.Length)},
			lineprotocol.Field{Key: "type", Value: token.TokenType},
			lineprotocol.Field{Key: "modifiers", Value: strings.Join(token.TokenModifiers, ",")},
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
	return newManagerToolWithWindowsColdInstallTimeoutAndDecodeError("structure", middleware.TierSlow, registry, decodeStrict, invalidStructureParams, func(ctx context.Context, registry lspmanager.Registry, req structureParams) (any, error) {
		if err := validateStructureLanguageParameters(req); err != nil {
			return nil, err
		}
		// Resolve the manager lazily per action: workspace_symbol can use
		// the "workspace_language" parameter instead of "file_path", so we must not
		// call GetManagerForFile unconditionally.
		resolveManager := func() (lspmanager.Manager, error) {
			return managerForFile(ctx, registry, req.FilePath, req.LanguageID)
		}
		return dispatchToolAction(ctx, "structure", req.Action, req, map[string]actionHandler[structureParams]{
			"document_symbol": func(ctx context.Context, req structureParams) (any, error) {
				return runDocumentSymbolAction(ctx, req, resolveManager)
			},
			"workspace_symbol": func(ctx context.Context, req structureParams) (any, error) {
				mgr, languageID, err := resolveWorkspaceSymbolManager(ctx, registry, req.FilePath, req.WorkspaceLanguage, req.LanguageID)
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

// validateStructureLanguageParameters 强制 workspace_language、file_path 与 language_id 的 action 边界。
func validateStructureLanguageParameters(req structureParams) error {
	workspaceLanguage := normalizeWorkspaceLanguage(req.WorkspaceLanguage)
	filePath := strings.TrimSpace(req.FilePath)
	languageID := normalizeWorkspaceLanguage(req.LanguageID)
	if languageID != "" && filePath == "" {
		return invalidStructureParams(errors.New("language_id is only valid with file_path; remove language_id"))
	}
	if req.Action != "workspace_symbol" && workspaceLanguage != "" {
		return invalidStructureParams(errors.New("workspace_language is only valid for workspace_symbol"))
	}
	return nil
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

// resolveWorkspaceSymbolManager 根据 workspace_language 或 file_path 选择 workspace_symbol 的 manager。
// 两个定位方式必须二选一，避免目录路径被误当成源码文件启动语言服务。
func resolveWorkspaceSymbolManager(ctx context.Context, registry lspmanager.Registry, filePath, workspaceLanguage, languageID string) (lspmanager.Manager, string, error) {
	workspaceLanguage = normalizeWorkspaceLanguage(workspaceLanguage)
	languageID = normalizeWorkspaceLanguage(languageID)
	filePath = strings.TrimSpace(filePath)
	if (filePath == "") == (workspaceLanguage == "") {
		return nil, "", invalidStructureParams(errors.New("exactly one of file_path or workspace_language is required"))
	}
	if workspaceLanguage != "" && languageID != "" {
		return nil, "", invalidStructureParams(errors.New("language_id is only valid with file_path; remove language_id"))
	}
	if workspaceLanguage != "" {
		return resolveWorkspaceLanguageManager(ctx, registry, workspaceLanguage)
	}
	return resolveWorkspaceFileManager(ctx, registry, filePath, languageID)
}

func resolveWorkspaceLanguageManager(ctx context.Context, registry lspmanager.Registry, workspaceLanguage string) (lspmanager.Manager, string, error) {
	if workspaceLanguage == sqliteSQLLanguageID {
		return nil, "", invalidStructureParams(errors.New("SQL workspace_symbol requires file_path so the SQLite sqlc owner can be validated"))
	}
	manager, err := registry.GetManagerForLanguage(ctx, workspaceLanguage)
	return manager, workspaceLanguage, err
}

func resolveWorkspaceFileManager(ctx context.Context, registry lspmanager.Registry, filePath, languageID string) (lspmanager.Manager, string, error) {
	if err := validateWorkspaceSymbolFilePath(filePath); err != nil {
		return nil, "", invalidStructureParams(err)
	}
	if languageID == "" {
		languageID = lspmanager.DetectLanguageID(workspaceSymbolPathForValidation(filePath))
	}
	manager, err := managerForFile(ctx, registry, filePath, languageID)
	if err != nil {
		if errors.Is(err, lspmanager.ErrUnsupportedLanguage) {
			return nil, "", errors.New("path must point to a source file with a configured language server; use workspace_language for workspace-wide search, and use file/grep for docs or config files")
		}
		return nil, "", err
	}
	return manager, languageID, nil
}

// invalidStructureParams 将 structure 入参错误映射为稳定的不可重试错误。
func invalidStructureParams(err error) error {
	return common.NewCodedToolError("invalid_params", err, false,
		"choose-file_path-or-workspace_language; use-language_id-only-as-a-file_path-server-override")
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
		if limit == protocol.XRefResultLimit {
			resp.FailureReason = "document_symbol_hard_limit_reached"
			resp.EffectiveLimit = limit
			resp.NextStep = "narrow_file_or_symbol_scope"
			resp.Hint = fmt.Sprintf("next: document_symbol reached the protocol hard limit (%d); narrow the file/symbol scope", limit)
		}
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
	if manager == nil {
		return nil, errManagerUnavailable
	}
	limit := format.WorkspaceSymbolLimit(req.MaxResults)
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
		Hint: "next: increase max_results or narrow query/workspace_language/file_path",
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
	legendManager, ok := manager.(lspmanager.SemanticTokensLegendManager)
	if !ok {
		return nil, semanticTokensProtocolError(errors.New("manager does not expose semantic tokens legend"))
	}
	tokenTypes, tokenModifiers, err := legendManager.SemanticTokensLegend(ctx, filePath)
	if err != nil {
		if errors.Is(err, lspmanager.ErrSemanticTokensLegendUnavailable) {
			return nil, common.NewCodedToolError("capability_unsupported", err, false,
				"next: use a language server that advertises semanticTokensProvider.legend")
		}
		return nil, semanticTokensProtocolError(err)
	}
	legend := protocol.SemanticTokensLegend{TokenTypes: tokenTypes, TokenModifiers: tokenModifiers}
	limit := shared.ClampLimit(req.MaxResults, 1, protocol.SemanticTokenResultLimit, protocol.SemanticTokenResultLimit)
	data, total, err := decodeSemanticTokenData(result, legend, limit)
	if err != nil {
		return nil, semanticTokensProtocolError(err)
	}
	response := semanticTokenListResponse{Legend: legend, Data: data, Total: total, Truncated: len(data) < total}
	if response.Truncated {
		response.Hint = "next: increase max_results or narrow file scope"
	}
	return response, nil
}

func semanticTokensProtocolError(err error) error {
	return common.NewCodedToolError("lsp_protocol_error", err, false,
		"retry-after-server-initialize-and-verify-semanticTokensProvider.legend-and-token-data")
}

// validateWorkspaceSymbolFilePath 拒绝目录路径，避免 workspace_symbol 扫描语义不明确。
func validateWorkspaceSymbolFilePath(filePath string) error {
	filePath = strings.TrimSpace(filePath)
	if stat, err := os.Stat(workspaceSymbolPathForValidation(filePath)); err == nil && stat.IsDir() {
		return errors.New("directory path is not supported for workspace_symbol; use workspace_language instead")
	}
	return nil
}

// normalizeWorkspaceLanguage 标准化 workspace_language 或 language_id 参数。
func normalizeWorkspaceLanguage(raw string) string {
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

func decodeSemanticTokenData(result *protocol.SemanticTokensResult, legend protocol.SemanticTokensLegend, limit int) ([]protocol.DecodedSemanticToken, int, error) {
	if len(legend.TokenTypes) == 0 {
		return nil, 0, errors.New("semantic tokens legend tokenTypes is empty")
	}
	var data []int
	if result != nil {
		data = result.Data
	}
	if len(data)%5 != 0 {
		return nil, 0, fmt.Errorf("semantic tokens data length %d is not divisible by 5", len(data))
	}
	total := len(data) / 5
	decoded := make([]protocol.DecodedSemanticToken, 0, min(limit, total))
	line, column := 0, 0
	for tuple := range total {
		values := data[tuple*5 : tuple*5+5]
		modifiers, err := validateSemanticTokenTuple(values, tuple, legend)
		if err != nil {
			return nil, 0, err
		}
		line, column, err = advanceSemanticTokenPosition(line, column, values, tuple)
		if err != nil {
			return nil, 0, err
		}
		if tuple < limit {
			decoded = append(decoded, protocol.DecodedSemanticToken{
				Line: format.FromLSP(line), StartCharacter: format.FromLSP(column), Length: values[2],
				TokenType: legend.TokenTypes[values[3]], TokenModifiers: modifiers,
			})
		}
	}
	return decoded, total, nil
}

func validateSemanticTokenTuple(values []int, tuple int, legend protocol.SemanticTokensLegend) ([]string, error) {
	for _, value := range values {
		if value < 0 {
			return nil, fmt.Errorf("semantic token tuple %d contains a negative value", tuple)
		}
	}
	if values[3] >= len(legend.TokenTypes) {
		return nil, fmt.Errorf("semantic token tuple %d tokenType index %d exceeds legend", tuple, values[3])
	}
	modifiers, err := decodeSemanticTokenModifiers(values[4], legend.TokenModifiers)
	if err != nil {
		return nil, fmt.Errorf("semantic token tuple %d: %w", tuple, err)
	}
	return modifiers, nil
}

func advanceSemanticTokenPosition(line, column int, values []int, tuple int) (int, int, error) {
	if values[0] == 0 {
		column += values[1]
	} else {
		line += values[0]
		column = values[1]
	}
	if line < 0 || column < 0 {
		return 0, 0, fmt.Errorf("semantic token tuple %d position overflows", tuple)
	}
	return line, column, nil
}

func decodeSemanticTokenModifiers(bitset int, legend []string) ([]string, error) {
	modifiers := make([]string, 0, len(legend))
	for index := 0; bitset > 0; index, bitset = index+1, bitset>>1 {
		if bitset&1 == 0 {
			continue
		}
		if index >= len(legend) {
			return nil, fmt.Errorf("modifier bit %d exceeds legend", index)
		}
		modifiers = append(modifiers, legend[index])
	}
	return modifiers, nil
}
