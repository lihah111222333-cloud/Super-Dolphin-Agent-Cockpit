package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

type xrefParams struct {
	Action             string `json:"action"`
	Pos                string `json:"pos"`
	LanguageID         string `json:"language_id,omitempty"`
	Direction          string `json:"direction"`
	IncludeDeclaration *bool  `json:"include_declaration"`
	MaxResults         int    `json:"max_results"`
}

// NewXRefHandler 创建x引用处理器。
func NewXRefHandler(registry lspmanager.Registry) ToolHandler {
	return newManagerTool("xref", middleware.TierNormal, registry, decodeLenient, func(ctx context.Context, registry lspmanager.Registry, req xrefParams) (any, error) {
		filePath, position, err := resolveFilePositionRequest(ctx, filePositionParams{
			Pos:        req.Pos,
			LanguageID: req.LanguageID,
		})
		if err != nil {
			return nil, err
		}
		manager, err := managerForFile(ctx, registry, filePath, req.LanguageID)
		if err != nil {
			return nil, err
		}
		funcEnricher := newFuncRangeEnricher(ctx, registry)
		return dispatchToolAction(ctx, "xref", req.Action, req, map[string]actionHandler[xrefParams]{
			"references": func(ctx context.Context, req xrefParams) (any, error) {
				return runReferences(ctx, manager, filePath, position, req, funcEnricher)
			},
			"call_hierarchy": func(ctx context.Context, req xrefParams) (any, error) {
				return runCallHierarchy(ctx, manager, filePath, position, req)
			},
			"type_hierarchy": func(ctx context.Context, req xrefParams) (any, error) {
				return runTypeHierarchy(ctx, manager, filePath, position, req)
			},
		})
	})
}

func runReferences(
	ctx context.Context,
	manager lspmanager.Manager,
	filePath string,
	position protocol.Position,
	req xrefParams,
	enricher format.SymbolProvider,
) (any, error) {
	includeDeclaration := true
	if req.IncludeDeclaration != nil {
		includeDeclaration = *req.IncludeDeclaration
	}
	limit := format.ReferencesLimit(req.MaxResults, format.VerbosityCompact)
	results, err := manager.References(ctx, filePath, position, includeDeclaration)
	if err != nil {
		return nil, err
	}
	format.EnrichLocationResultsWithFuncRange(results, enricher)
	total := len(results)
	results = limitSlice(results, limit)
	grouped := groupLocationsByFile(ctx, results, total)
	if total == 0 {
		grouped.Hint = "no references found"
	}
	return grouped, nil
}

// runCallHierarchy 运行call层级。
func runCallHierarchy(
	ctx context.Context,
	manager lspmanager.Manager,
	filePath string,
	position protocol.Position,
	req xrefParams,
) (any, error) {
	direction, err := normalizeCallHierarchyDirection(req.Direction)
	if err != nil {
		return nil, err
	}
	languageID := firstNonEmpty(req.LanguageID, lspmanager.DetectLanguageID(filePath))
	if limitedDocumentFallbackLanguage(languageID) != "" {
		return unsupportedCapabilityEmptyResult("call hierarchy", languageID), nil
	}
	results, err := manager.CallHierarchy(ctx, filePath, position, direction)
	if isUnsupportedCapability(err) {
		return unsupportedCapabilityEmptyResult("call hierarchy", languageID), nil
	}
	if err != nil {
		return nil, err
	}
	limit := shared.ClampLimit(req.MaxResults, 1, protocol.XRefResultLimit, protocol.XRefResultLimit)
	total := len(results)
	hint := "next: increase max_results or narrow the target position"
	list := format.NewCompactList(compactCallHierarchyResults(ctx, limitSlice(results, limit)), total, hint)
	if total == 0 {
		list.Hint = emptyCallHierarchyHint(filePath, req.LanguageID)
	}
	return list, nil
}

func runTypeHierarchy(
	ctx context.Context,
	manager lspmanager.Manager,
	filePath string,
	position protocol.Position,
	req xrefParams,
) (any, error) {
	direction, err := normalizeTypeHierarchyDirection(req.Direction)
	if err != nil {
		return nil, err
	}
	results, err := manager.TypeHierarchy(ctx, filePath, position, direction)
	if isUnsupportedCapability(err) {
		return unsupportedCapabilityEmptyResult("type hierarchy"), nil
	}
	if err != nil {
		return nil, typeHierarchyTargetError(err)
	}
	return renderListResult(results, shared.ClampLimit(req.MaxResults, 1, protocol.XRefResultLimit, protocol.XRefResultLimit), "no type hierarchy found", func(items []protocol.TypeHierarchyResult, _ int) any {
		return format.NormalizeForDisplay(items)
	})
}

func normalizeCallHierarchyDirection(raw string) (string, error) {
	switch value := strings.ToLower(strings.TrimSpace(raw)); value {
	case "", "incoming", "outgoing", "both":
		return value, nil
	default:
		return "", fmt.Errorf("invalid call_hierarchy direction %q", raw)
	}
}

func normalizeTypeHierarchyDirection(raw string) (string, error) {
	switch value := strings.ToLower(strings.TrimSpace(raw)); value {
	case "", "both":
		return "", nil
	case "supertypes", "subtypes":
		return value, nil
	default:
		return "", fmt.Errorf("invalid type_hierarchy direction %q", raw)
	}
}

func emptyCallHierarchyHint(filePath, languageID string) string {
	if isJSTSCallHierarchyLanguage(languageIDForCallHierarchyHint(filePath, languageID)) {
		return "no call hierarchy found; partial reason: JS/TS language server returned no prepare item after document bootstrap. next: retry with the cursor on a function or method identifier; if this persists, run diagnostics and verify package.json/tsconfig plus installed dependencies"
	}
	return "no call hierarchy found"
}

func languageIDForCallHierarchyHint(filePath, languageID string) string {
	if normalized := normalizeLanguageIDOverride(languageID); normalized != "" {
		return normalized
	}
	return lspmanager.DetectLanguageID(filePath)
}

func isJSTSCallHierarchyLanguage(languageID string) bool {
	switch strings.ToLower(strings.TrimSpace(languageID)) {
	case "javascript", "javascriptreact", "typescript", "typescriptreact":
		return true
	default:
		return false
	}
}
