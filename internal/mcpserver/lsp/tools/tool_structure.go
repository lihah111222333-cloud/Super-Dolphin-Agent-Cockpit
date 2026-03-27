package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/format"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/gopls"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
)

type structureParams struct {
	Action     string `json:"action"`
	FilePath   string `json:"file_path"`
	Query      string `json:"query"`
	Language   string `json:"language"`
	Verbosity  string `json:"verbosity"`
	MaxResults int    `json:"max_results"`
}

func NewStructureHandler(manager gopls.Manager) ToolHandler {
	if manager == nil {
		return missingManagerHandler()
	}
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		req, err := decodeParams[structureParams](params)
		if err != nil {
			return nil, err
		}
		switch normalizeAction(req.Action) {
		case "document_symbol":
			return runDocumentSymbols(ctx, manager, req)
		case "workspace_symbol":
			return runWorkspaceSymbols(ctx, manager, req)
		case "folding_range":
			return runFoldingRanges(ctx, manager, req)
		case "semantic_tokens":
			return runSemanticTokens(ctx, manager, req)
		default:
			return nil, fmt.Errorf("unsupported structure action %q", req.Action)
		}
	}
}

func runDocumentSymbols(
	ctx context.Context,
	manager gopls.Manager,
	req structureParams,
) (any, error) {
	filePath, err := requireFilePath(req.FilePath)
	if err != nil {
		return nil, err
	}
	results, err := manager.DocumentSymbol(ctx, filePath)
	if err != nil {
		return nil, err
	}
	results = limitDocumentSymbols(results, clampResultLimit(req.MaxResults, protocol.XRefResultLimit))
	if len(results) == 0 {
		return "no symbols found", nil
	}
	return format.NormalizeForDisplay(results), nil
}

func runWorkspaceSymbols(
	ctx context.Context,
	manager gopls.Manager,
	req structureParams,
) (any, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, errors.New("query is required")
	}
	if err := validateWorkspaceSymbolScope(req.FilePath, req.Language); err != nil {
		return nil, err
	}
	verbosity := format.NormalizeVerbosity(req.Verbosity)
	limit := format.WorkspaceSymbolLimit(req.MaxResults, verbosity)
	results, err := manager.WorkspaceSymbol(ctx, query)
	if err != nil {
		return nil, err
	}
	total := len(results)
	results = limitSlice(results, limit)
	if len(results) == 0 {
		return "no symbols found", nil
	}
	if verbosity == format.VerbosityFull {
		return format.NormalizeForDisplay(results), nil
	}
	return format.NewCompactList(format.CompactWorkspaceSymbols(results), total), nil
}

func runFoldingRanges(
	ctx context.Context,
	manager gopls.Manager,
	req structureParams,
) (any, error) {
	filePath, err := requireFilePath(req.FilePath)
	if err != nil {
		return nil, err
	}
	results, err := manager.FoldingRange(ctx, filePath)
	if err != nil {
		return nil, err
	}
	results = limitSlice(results, clampResultLimit(req.MaxResults, protocol.XRefResultLimit))
	if len(results) == 0 {
		return "no folding ranges found", nil
	}
	return format.NormalizeForDisplay(results), nil
}

func runSemanticTokens(
	ctx context.Context,
	manager gopls.Manager,
	req structureParams,
) (any, error) {
	filePath, err := requireFilePath(req.FilePath)
	if err != nil {
		return nil, err
	}
	result, err := manager.SemanticTokens(ctx, filePath)
	if err != nil {
		return nil, err
	}
	limit := format.ResolveResultLimit(req.MaxResults, req.Verbosity, protocol.SemanticTokenResultLimit)
	result = capSemanticTokens(result, limit)
	if result == nil || (len(result.Data) == 0 && len(result.Decoded) == 0) {
		return "no semantic tokens found", nil
	}
	return format.NormalizeForDisplay(result), nil
}

func validateWorkspaceSymbolScope(filePath, language string) error {
	filePath = strings.TrimSpace(filePath)
	language = normalizeWorkspaceSymbolLanguage(language)
	if (filePath == "") == (language == "") {
		return errors.New("exactly one of file_path or language is required")
	}
	if language != "" {
		if !supportsWorkspaceSymbolLanguage(language) {
			return fmt.Errorf("language %q is not managed by gopls", language)
		}
		return nil
	}
	if stat, err := os.Stat(workspaceSymbolPathForValidation(filePath)); err == nil && stat.IsDir() {
		return errors.New("directory path is not supported for workspace_symbol; use language instead")
	}
	if !supportsWorkspaceSymbolPath(filePath) {
		return errors.New("path must point to a source file with a configured language server; use language for workspace-wide search, and use lsp_file/lsp_grep for docs or config files")
	}
	return nil
}

func normalizeWorkspaceSymbolLanguage(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func supportsWorkspaceSymbolLanguage(language string) bool {
	switch normalizeWorkspaceSymbolLanguage(language) {
	case "go", "gomod", "gosum", "gowork":
		return true
	default:
		return false
	}
}

func supportsWorkspaceSymbolPath(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "go.mod", "go.sum", "go.work":
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return true
	default:
		return false
	}
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
