package tools

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type structureParams struct {
	Action     string `json:"action"`
	FilePath   string `json:"file_path"`
	Path       string `json:"path"`
	LanguageID string `json:"language_id,omitempty"`
	Query      string `json:"query"`
	Language   string `json:"language"`
	Verbosity  string `json:"verbosity"`
	MaxResults int    `json:"max_results"`
}

func NewStructureHandler(registry lspmanager.Registry) ToolHandler {
	return newManagerTool("structure", middleware.TierNormal, registry, decodeStrict, func(ctx context.Context, registry lspmanager.Registry, req structureParams) (any, error) {
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// resolveWorkspaceSymbolManager picks the right manager based on language or
// file_path and returns the language that WorkspaceSymbol should use.
func resolveWorkspaceSymbolManager(ctx context.Context, registry lspmanager.Registry, filePath, language string) (lspmanager.Manager, string, error) {
	language = normalizeWorkspaceSymbolLanguage(language)
	filePath = strings.TrimSpace(filePath)
	if (filePath == "") == (language == "") {
		return nil, "", errors.New("exactly one of file_path or language is required")
	}
	if language != "" {
		manager, err := registry.GetManagerForLanguage(ctx, language)
		if err != nil {
			return nil, "", err
		}
		return manager, language, nil
	}
	if err := validateWorkspaceSymbolFilePath(filePath); err != nil {
		return nil, "", err
	}
	manager, err := managerForFile(ctx, registry, filePath, "")
	if err != nil {
		if errors.Is(err, lspmanager.ErrUnsupportedLanguage) {
			return nil, "", errors.New("path must point to a source file with a configured language server; use language for workspace-wide search, and use file/grep for docs or config files")
		}
		return nil, "", err
	}
	return manager, lspmanager.DetectLanguageID(workspaceSymbolPathForValidation(filePath)), nil
}

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
	results = limitDocumentSymbols(results, shared.ClampLimit(req.MaxResults, 1, protocol.XRefResultLimit, protocol.XRefResultLimit))
	return renderListResult(results, protocol.XRefResultLimit, "no symbols found", func(items []protocol.DocumentSymbol, _ int) any {
		return format.NormalizeForDisplay(items)
	})
}

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
	verbosity := format.NormalizeVerbosity(req.Verbosity)
	limit := format.WorkspaceSymbolLimit(req.MaxResults, verbosity)
	results, err := manager.WorkspaceSymbol(ctx, query, languageID)
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
	return renderByVerbosity(results, total, verbosity,
		func(items []protocol.WorkspaceSymbolResult) any { return format.NormalizeForDisplay(items) },
		func(items []protocol.WorkspaceSymbolResult, total int) any {
			return format.NewCompactList(format.CompactWorkspaceSymbols(items), total)
		},
	), nil
}

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

func validateWorkspaceSymbolFilePath(filePath string) error {
	filePath = strings.TrimSpace(filePath)
	if stat, err := os.Stat(workspaceSymbolPathForValidation(filePath)); err == nil && stat.IsDir() {
		return errors.New("directory path is not supported for workspace_symbol; use language instead")
	}
	return nil
}

func normalizeWorkspaceSymbolLanguage(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func workspaceSymbolPathForValidation(path string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "file://") {
		if absPath, err := format.AbsolutePathFromURI(path); err == nil {
			return absPath
		}
	}
	return path
}

func limitDocumentSymbols(symbols []protocol.DocumentSymbol, limit int) []protocol.DocumentSymbol {
	if len(symbols) == 0 || limit <= 0 {
		return nil
	}
	remaining := limit
	return limitDocumentSymbolNodes(symbols, &remaining)
}

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
