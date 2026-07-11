package tools

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// structureParams 是 structure 工具的入参，兼容 file_path/path 和 language 写法。
type structureParams struct {
	Action     string `json:"action"`
	FilePath   string `json:"file_path"`
	Path       string `json:"path"`
	LanguageID string `json:"language_id,omitempty"`
	Query      string `json:"query"`
	Language   string `json:"language"`
	MaxResults int    `json:"max_results"`
}

// documentSymbolListResponse 是 document_symbol 的分页响应。
type documentSymbolListResponse struct {
	Data      []protocol.DocumentSymbol `json:"data"`
	Total     int                       `json:"total"`
	Showing   int                       `json:"showing"`
	Truncated bool                      `json:"truncated,omitempty"`
	Hint      string                    `json:"hint,omitempty"`
}

// NewStructureHandler 创建 structure 工具处理器，按 action 延迟选择文件或语言级 manager。
func NewStructureHandler(registry lspmanager.Registry) ToolHandler {
	return newManagerTool("structure", middleware.TierSlow, registry, decodeLenient, func(ctx context.Context, registry lspmanager.Registry, req structureParams) (any, error) {
		req.FilePath = firstNonEmpty(req.FilePath, req.Path)
		// Resolve the manager lazily per action: workspace_symbol can use
		// the "language" parameter instead of "file_path", so we must not
		// call GetManagerForFile unconditionally.
		resolveManager := func() (lspmanager.Manager, error) {
			return managerForFile(ctx, registry, req.FilePath, req.LanguageID)
		}
		return dispatchToolAction(ctx, "structure", req.Action, req, map[string]actionHandler[structureParams]{
			"document_symbol": func(ctx context.Context, req structureParams) (any, error) {
				mgr, err := resolveManager()
				if err != nil {
					return nil, err
				}
				return runDocumentSymbols(ctx, mgr, req)
			},
			"workspace_symbol": func(ctx context.Context, req structureParams) (any, error) {
				mgr, languageID, err := resolveWorkspaceSymbolManager(ctx, registry, req.FilePath, firstNonEmpty(req.Language, req.LanguageID))
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
func resolveWorkspaceSymbolManager(ctx context.Context, registry lspmanager.Registry, filePath, language string) (lspmanager.Manager, string, error) {
	language = normalizeWorkspaceSymbolLanguage(language)
	filePath = strings.TrimSpace(filePath)
	if (filePath == "") == (language == "") {
		return nil, "", errors.New("exactly one of file_path or language is required")
	}
	if language != "" {
		if limitedDocumentFallbackLanguage(language) != "" {
			return nil, language, nil
		}
		manager, err := registry.GetManagerForLanguage(ctx, language)
		if err != nil {
			return nil, "", err
		}
		return manager, language, nil
	}
	if err := validateWorkspaceSymbolFilePath(filePath); err != nil {
		return nil, "", err
	}
	language = lspmanager.DetectLanguageID(workspaceSymbolPathForValidation(filePath))
	if limitedDocumentFallbackLanguage(language) != "" {
		return nil, language, nil
	}
	manager, err := managerForFile(ctx, registry, filePath, "")
	if err != nil {
		if errors.Is(err, lspmanager.ErrUnsupportedLanguage) {
			return nil, "", errors.New("path must point to a source file with a configured language server; use language for workspace-wide search, and use file/grep for docs or config files")
		}
		return nil, "", err
	}
	return manager, language, nil
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
	limit := shared.ClampLimit(req.MaxResults, 1, protocol.XRefResultLimit, protocol.XRefResultLimit)
	results = limitDocumentSymbols(results, limit)
	showing := countDocumentSymbolNodes(results)
	if showing == 0 {
		return emptyListEnvelope{
			Success: true,
			Data:    []any{},
			Meta:    resultMeta{Count: 0, Message: "no symbols found"},
		}, nil
	}
	resp := documentSymbolListResponse{
		Data:      format.NormalizeForDisplay(results),
		Total:     total,
		Showing:   showing,
		Truncated: showing < total,
	}
	if resp.Truncated {
		resp.Hint = "next: increase max_results or narrow the file/symbol scope"
	}
	return resp, nil
}

// runWorkspaceSymbols 运行 workspace_symbol；传 file_path 时先 bootstrap 目标文件。
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
	if limitedDocumentFallbackLanguage(languageID) != "" {
		return unsupportedCapabilityEmptyResult("workspace symbol", languageID), nil
	}
	if manager == nil {
		return nil, errManagerUnavailable
	}
	limit := format.WorkspaceSymbolLimit(req.MaxResults, format.VerbosityCompact)
	if err := bootstrapWorkspaceSymbolTarget(ctx, manager, req.FilePath); err != nil {
		return nil, err
	}
	results, err := manager.WorkspaceSymbol(ctx, query, languageID)
	if isUnsupportedCapability(err) {
		return unsupportedCapabilityEmptyResult("workspace symbol", languageID), nil
	}
	if err != nil {
		return nil, err
	}
	total := len(results)
	results = limitSlice(results, limit)
	if len(results) == 0 {
		return emptyListEnvelope{
			Success: true,
			Data:    []any{},
			Meta:    resultMeta{Count: 0, Message: "no symbols found"},
		}, nil
	}
	return format.NewCompactList(
		format.CompactWorkspaceSymbols(results),
		total,
		"next: increase max_results or narrow query/language",
	), nil
}

// bootstrapWorkspaceSymbolTarget 在 workspace_symbol 前打开目标文件，帮助语言服务建立项目上下文。
func bootstrapWorkspaceSymbolTarget(ctx context.Context, manager lspmanager.Manager, filePath string) error {
	if strings.TrimSpace(filePath) == "" {
		return nil
	}
	resolved, err := resolveFilePath(ctx, filePath)
	if err != nil {
		return err
	}
	return manager.BootstrapDocument(ctx, resolved)
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
	return renderListResult(results, shared.ClampLimit(req.MaxResults, 1, protocol.XRefResultLimit, protocol.XRefResultLimit), "no folding ranges found", func(items []protocol.FoldingRange, _ int) any {
		return format.NormalizeForDisplay(items)
	})
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
