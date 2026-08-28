package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
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

// NewStructureHandler 创建 structure 工具处理器，按 action 延迟选择文件或语言级 manager。
func NewStructureHandler(registry lspmanager.Registry) ToolHandler {
	handler := newManagerToolWithWindowsColdInstallTimeoutAndDecodeError("structure", middleware.TierSlow, registry, decodeStrict, invalidStructureParams, func(ctx context.Context, registry lspmanager.Registry, req structureParams) (any, error) {
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
	return deduplicateSemanticToolHandler("structure", registry, handler)
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
	if recoded := recodeStrictSchemaError(
		err,
		"invalid_params",
		"choose-file_path-or-workspace_language; use-language_id-only-as-a-file_path-server-override",
	); recoded != nil {
		return recoded
	}
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
	// The structured response and its plain-text projection must share one
	// displayable symbol tree. Filter/promote first so hidden nodes cannot
	// consume the result budget or inflate total/showing in either projection.
	results = filterDocumentSymbols(results)
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
	if total == 0 {
		resp.Hint = "no symbols found in document; verify file path and content, or check if language server supports document symbols"
	} else if resp.Truncated {
		resp.Hint = "next: increase max_results or narrow the file/symbol scope"
		if limit == protocol.XRefResultLimit {
			resp.FailureReason = "document_symbol_hard_limit_reached"
			resp.EffectiveLimit = limit
			resp.NextStep = "narrow_file_or_symbol_scope"
			resp.Hint = fmt.Sprintf("next: document_symbol reached the protocol hard limit (%d); narrow the file/symbol scope", limit)
		}
		continuation, continuationErr := documentSymbolContinuation(req, total, showing, limit)
		if continuationErr != nil {
			return nil, continuationErr
		}
		resp.Continuation = continuation
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
	items, err = prepareWorkspaceSymbolItems(ctx, items, req, query, matchMode)
	if err != nil {
		return nil, err
	}
	return renderWorkspaceSymbolResponse(req, items, matchMode, limit)
}

func prepareWorkspaceSymbolItems(ctx context.Context, items []format.CompactWorkspaceSymbol, req structureParams, query, matchMode string) ([]format.CompactWorkspaceSymbol, error) {
	if strings.TrimSpace(req.FilePath) != "" {
		filtered, err := filterWorkspaceSymbolsForFile(ctx, items, req.FilePath)
		if err != nil {
			return nil, err
		}
		items = filtered
	}
	return selectWorkspaceSymbolMatches(items, query, matchMode), nil
}

func renderWorkspaceSymbolResponse(req structureParams, items []format.CompactWorkspaceSymbol, matchMode string, limit int) (workspaceSymbolListResponse, error) {
	total := len(items)
	items = limitSlice(items, limit)
	response := workspaceSymbolListResponse{Data: items, Total: total, Showing: len(items), Truncated: len(items) < total}
	if total == 0 {
		response.Hint = workspaceSymbolEmptyHint(matchMode)
		return response, nil
	}
	if !response.Truncated {
		return response, nil
	}
	response.Hint = workspaceSymbolTruncatedHint(matchMode)
	continuation, err := workspaceSymbolContinuation(req, matchMode, total, len(items), limit)
	if err != nil {
		return workspaceSymbolListResponse{}, err
	}
	response.Continuation = continuation
	return response, nil
}

func workspaceSymbolEmptyHint(matchMode string) string {
	if matchMode == workspaceSymbolMatchExact {
		return "no symbols found matching exact query; check symbol casing, retry with match_mode=fuzzy, or use document_symbol for exact file-level inspection"
	}
	return "no symbols found; check symbol name or casing, or use document_symbol on target file"
}

func workspaceSymbolTruncatedHint(matchMode string) string {
	if matchMode == workspaceSymbolMatchFuzzy {
		return "next: workspace_symbol results truncated; narrow query with container prefix, use match_mode=exact, or increase max_results"
	}
	return "next: workspace_symbol results truncated; narrow query with container prefix or file_path, or increase max_results"
}

// normalizeWorkspaceSymbolMatchMode 收敛 workspace_symbol 匹配模式，未知值立即失败。缺省时默认使用 fuzzy/smart ranking。
func normalizeWorkspaceSymbolMatchMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", workspaceSymbolMatchFuzzy:
		return workspaceSymbolMatchFuzzy, nil
	case workspaceSymbolMatchExact:
		return workspaceSymbolMatchExact, nil
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

// selectWorkspaceSymbolMatches 按照 Smart Ranking（Exact 100 > Prefix 80 > WordBoundary/CamelCase 60 > Substring 40）排序。
// matchMode=exact 仅保留 Exact 100 分匹配项；默认和 fuzzy 模式按权重降序排列所有候选项。
func selectWorkspaceSymbolMatches(items []format.CompactWorkspaceSymbol, query, matchMode string) []format.CompactWorkspaceSymbol {
	if matchMode == workspaceSymbolMatchExact {
		exact := make([]format.CompactWorkspaceSymbol, 0, len(items))
		for _, item := range items {
			if scoreWorkspaceSymbol(item.Name, item.Container, query) == 100 {
				exact = append(exact, item)
			}
		}
		return exact
	}

	type scoredSymbol struct {
		symbol format.CompactWorkspaceSymbol
		score  int
	}
	scored := make([]scoredSymbol, 0, len(items))
	for _, item := range items {
		score := scoreWorkspaceSymbol(item.Name, item.Container, query)
		if score > 0 {
			scored = append(scored, scoredSymbol{symbol: item, score: score})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	result := make([]format.CompactWorkspaceSymbol, len(scored))
	for i, s := range scored {
		result[i] = s.symbol
	}
	return result
}

// scoreWorkspaceSymbol 计算符号与 query 的 Smart Ranking 权重得分。
// 权重降序：Exact 100 > Prefix 80 > WordBoundary/CamelCase 60 > Substring 40 > Server Fuzzy 20.
func scoreWorkspaceSymbol(name, container, query string) int {
	nameTrim := strings.TrimSpace(name)
	queryTrim := strings.TrimSpace(query)
	if nameTrim == "" || queryTrim == "" {
		return 0
	}
	leafName := extractLeafSymbol(nameTrim)
	if exactWorkspaceSymbolMatch(nameTrim, container, leafName, queryTrim) {
		return 100
	}
	lowerName := strings.ToLower(nameTrim)
	lowerQuery := strings.ToLower(queryTrim)
	lowerLeaf := strings.ToLower(leafName)
	if prefixWorkspaceSymbolMatch(lowerName, lowerLeaf, lowerQuery) {
		return 80
	}
	if boundaryWorkspaceSymbolMatch(nameTrim, leafName, lowerQuery) {
		return 60
	}
	if substringWorkspaceSymbolMatch(lowerName, lowerLeaf, container, lowerQuery) {
		return 40
	}
	return 20
}

// exactWorkspaceSymbolMatch 判断完整名称、容器限定名和叶名称的精确匹配。
func exactWorkspaceSymbolMatch(name, container, leaf, query string) bool {
	if name == query || strings.EqualFold(name, query) {
		return true
	}
	if container != "" {
		fullDot := container + "." + name
		fullColon := container + "::" + name
		if fullDot == query || strings.EqualFold(fullDot, query) ||
			fullColon == query || strings.EqualFold(fullColon, query) {
			return true
		}
	}
	return leaf != name && (leaf == query || strings.EqualFold(leaf, query))
}

// prefixWorkspaceSymbolMatch 判断名称和叶名称的前缀匹配。
func prefixWorkspaceSymbolMatch(name, leaf, query string) bool {
	return strings.HasPrefix(name, query) || strings.HasPrefix(leaf, query)
}

// boundaryWorkspaceSymbolMatch 判断分隔符、CamelCase 和缩写匹配。
func boundaryWorkspaceSymbolMatch(name, leaf, query string) bool {
	return isWordBoundaryMatch(name, query) || isWordBoundaryMatch(leaf, query) ||
		isCamelCaseAcronymMatch(name, query) || isCamelCaseAcronymMatch(leaf, query)
}

// substringWorkspaceSymbolMatch 判断名称、叶名称和容器的子串匹配。
func substringWorkspaceSymbolMatch(name, leaf, container, query string) bool {
	return strings.Contains(name, query) || strings.Contains(leaf, query) ||
		(container != "" && strings.Contains(strings.ToLower(container), query))
}

func extractLeafSymbol(name string) string {
	lastDot := strings.LastIndex(name, ".")
	lastColon := strings.LastIndex(name, "::")
	lastSlash := strings.LastIndex(name, "/")
	idx := max(lastDot, lastColon, lastSlash)
	if idx >= 0 && idx+1 < len(name) {
		if lastColon == idx && idx+2 < len(name) {
			return name[idx+2:]
		}
		return name[idx+1:]
	}
	return name
}

// isWordBoundaryMatch 检查 query 是否在 word boundary（分隔符后或 CamelCase 大写处）匹配。
func isWordBoundaryMatch(name, lowerQuery string) bool {
	lowerName := strings.ToLower(name)
	runes := []rune(name)
	lowerRunes := []rune(lowerName)
	queryRunes := []rune(lowerQuery)
	if len(queryRunes) == 0 || len(runes) < len(queryRunes) {
		return false
	}

	for i := range len(runes) - len(queryRunes) + 1 {
		if i > 0 && workspaceSymbolBoundaryAt(runes, lowerRunes, queryRunes, i) {
			return true
		}
	}
	return false
}

func workspaceSymbolBoundaryAt(runes, lowerRunes, queryRunes []rune, index int) bool {
	for offset := range len(queryRunes) {
		if lowerRunes[index+offset] != queryRunes[offset] {
			return false
		}
	}
	prevRune, currentRune := runes[index-1], runes[index]
	return isWordDelimiter(prevRune) || unicode.IsUpper(currentRune) && unicode.IsLower(prevRune)
}

// isWordDelimiter 判断字符是否是符号名称中的词边界分隔符。
func isWordDelimiter(r rune) bool {
	return r == '_' || r == '-' || r == '.' || r == ':' || r == '/' || r == '\\' || r == ' ' || r == '$' || r == '#'
}

// isCamelCaseAcronymMatch 检查 query 是否为 CamelCase 缩写（如 WS 匹配 WorkspaceSymbol）。
func isCamelCaseAcronymMatch(name, lowerQuery string) bool {
	var initials []rune
	for i, r := range name {
		if i == 0 && unicode.IsLetter(r) {
			initials = append(initials, unicode.ToLower(r))
		} else if unicode.IsUpper(r) {
			initials = append(initials, unicode.ToLower(r))
		}
	}
	if len(initials) == 0 {
		return false
	}
	return string(initials) == lowerQuery
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
	limit := shared.ClampLimit(req.MaxResults, 1, protocol.XRefResultLimit, protocol.XRefResultLimit)
	items := limitSlice(results, limit)
	response := foldingRangeListResponse{
		Data: items, Total: total, Showing: len(items), Truncated: len(items) < total,
	}
	if response.Truncated {
		response.Hint = "next: increase max_results or narrow file scope"
		continuation, continuationErr := fileStructureContinuation(
			"folding_range", req.FilePath, total, len(items), limit, protocol.XRefResultLimit,
		)
		if continuationErr != nil {
			return nil, continuationErr
		}
		response.Continuation = continuation
	} else if total == 0 {
		response.Hint = "no folding ranges found in file"
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
		continuation, continuationErr := fileStructureContinuation(
			"semantic_tokens", req.FilePath, total, len(data), limit, protocol.SemanticTokenResultLimit,
		)
		if continuationErr != nil {
			return nil, continuationErr
		}
		response.Continuation = continuation
	} else if total == 0 {
		response.Hint = "no semantic tokens found in file"
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

// filterDocumentSymbols 保留可用于文件骨架的符号，并提升被过滤节点的可见子节点。
//
// LSP servers may return local variables and expression-like nodes alongside
// declarations. The plain-text outline intentionally omits those nodes. Do
// that transformation before counting or limiting so structured Data and ROW
// records expose exactly the same set of symbols.
func filterDocumentSymbols(symbols []protocol.DocumentSymbol) []protocol.DocumentSymbol {
	return filterDocumentSymbolNodes(symbols, false)
}

// filterDocumentSymbolNodes 递归过滤符号并保持被过滤包装节点的可见子节点。
func filterDocumentSymbolNodes(symbols []protocol.DocumentSymbol, inFunction bool) []protocol.DocumentSymbol {
	if len(symbols) == 0 {
		return nil
	}

	filtered := make([]protocol.DocumentSymbol, 0, len(symbols))
	for _, symbol := range symbols {
		visible := isCoreStructureSymbol(symbol.Kind, inFunction)
		nextInFunction := inFunction
		if visible {
			nextInFunction = inFunction || isFunctionKind(symbol.Kind)
		}
		children := filterDocumentSymbolNodes(symbol.Children, nextInFunction)
		if visible {
			symbol.Children = children
			filtered = append(filtered, symbol)
			continue
		}
		// Keep the same depth the plain-text skeleton used when it skipped this
		// node. A declaration nested under an unknown wrapper remains useful.
		filtered = append(filtered, children...)
	}
	return filtered
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

// decodeSemanticTokenData 校验并解码 LSP 返回的相对语义 token 数据。
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
