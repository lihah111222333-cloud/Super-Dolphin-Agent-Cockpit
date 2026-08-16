package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// xrefParams 是 xref 工具的入参，覆盖引用、调用层级和类型层级查询。
type xrefParams struct {
	Action             string `json:"action"`
	Pos                string `json:"pos"`
	LanguageID         string `json:"language_id,omitempty"`
	Direction          string `json:"direction"`
	IncludeDeclaration *bool  `json:"include_declaration"`
	MaxResults         int    `json:"max_results"`
}

// NewXRefHandler 创建 xref 工具处理器，并为引用结果补充函数范围。
func NewXRefHandler(registry lspmanager.Registry) ToolHandler {
	return newManagerToolWithoutOuterTimeout("xref", middleware.TierNormal, registry, decodeLenient, func(ctx context.Context, registry lspmanager.Registry, req xrefParams) (any, error) {
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

// runReferences 查询符号引用，并按 compact 上限裁剪。
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
		return nil, enrichIdentifierNotFoundError(filePath, position, err)
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

// runCallHierarchy 查询调用层级；对不支持该能力的语言返回标准空结果。
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
	incoming, outgoing := compactCallHierarchyEdges(ctx, results)
	rows, total := selectCallHierarchyEdges(incoming, outgoing, direction, limit)
	hint := "next: increase max_results or narrow the target position"
	if total == 0 {
		hint = emptyCallHierarchyHint(filePath, req.LanguageID)
	}
	return hierarchyEdgeListResponse{Rows: rows, Total: total, Unit: "edge", Hint: hint}, nil
}

// runTypeHierarchy 查询类型层级，并把无效目标包装成可读错误。
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
	limit := shared.ClampLimit(req.MaxResults, 1, protocol.XRefResultLimit, protocol.XRefResultLimit)
	supertypes, subtypes := compactTypeHierarchyEdges(ctx, results)
	rows, total := selectTypeHierarchyEdges(supertypes, subtypes, direction, limit)
	return hierarchyEdgeListResponse{Rows: rows, Total: total, Unit: "type_edge", Hint: "next: increase max_results or narrow the target position"}, nil
}

func selectCallHierarchyEdges(incoming, outgoing []hierarchyEdgeRow, direction string, limit int) ([]hierarchyEdgeRow, int) {
	switch direction {
	case "incoming":
		return limitSlice(incoming, limit), len(incoming)
	case "outgoing":
		return limitSlice(outgoing, limit), len(outgoing)
	default:
		return alternateHierarchyEdges(incoming, outgoing, limit), len(incoming) + len(outgoing)
	}
}

func selectTypeHierarchyEdges(supertypes, subtypes []hierarchyEdgeRow, direction string, limit int) ([]hierarchyEdgeRow, int) {
	switch direction {
	case "supertypes":
		return limitSlice(supertypes, limit), len(supertypes)
	case "subtypes":
		return limitSlice(subtypes, limit), len(subtypes)
	default:
		return alternateHierarchyEdges(supertypes, subtypes, limit), len(supertypes) + len(subtypes)
	}
}

// alternateHierarchyEdges 在两个方向都有结果时稳定交替，避免一侧因上限饥饿。
func alternateHierarchyEdges(first, second []hierarchyEdgeRow, limit int) []hierarchyEdgeRow {
	selected := make([]hierarchyEdgeRow, 0, min(limit, len(first)+len(second)))
	for firstIndex, secondIndex := 0, 0; len(selected) < limit && (firstIndex < len(first) || secondIndex < len(second)); {
		if firstIndex < len(first) {
			selected = append(selected, first[firstIndex])
			firstIndex++
		}
		if len(selected) < limit && secondIndex < len(second) {
			selected = append(selected, second[secondIndex])
			secondIndex++
		}
	}
	return selected
}

// normalizeCallHierarchyDirection 校验 call_hierarchy direction 参数。
func normalizeCallHierarchyDirection(raw string) (string, error) {
	switch value := strings.ToLower(strings.TrimSpace(raw)); value {
	case "", "incoming", "outgoing", "both":
		return value, nil
	default:
		return "", fmt.Errorf("invalid call_hierarchy direction %q", raw)
	}
}

// normalizeTypeHierarchyDirection 校验 type_hierarchy direction 参数。
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

// emptyCallHierarchyHint 为 JS/TS bootstrap 不完整场景提供更具体的重试提示。
func emptyCallHierarchyHint(filePath, languageID string) string {
	if isJSTSCallHierarchyLanguage(languageIDForCallHierarchyHint(filePath, languageID)) {
		return "no call hierarchy found; partial reason: JS/TS language server returned no prepare item after document bootstrap. next: retry with the cursor on a function or method identifier; if this persists, run diagnostics and verify package.json/tsconfig plus installed dependencies"
	}
	return "no call hierarchy found"
}

// languageIDForCallHierarchyHint 选择提示用语言 ID，优先使用调用方显式覆盖。
func languageIDForCallHierarchyHint(filePath, languageID string) string {
	if normalized := normalizeLanguageIDOverride(languageID); normalized != "" {
		return normalized
	}
	return lspmanager.DetectLanguageID(filePath)
}

// isJSTSCallHierarchyLanguage 判断语言是否属于 JS/TS 家族。
func isJSTSCallHierarchyLanguage(languageID string) bool {
	switch strings.ToLower(strings.TrimSpace(languageID)) {
	case "javascript", "javascriptreact", "typescript", "typescriptreact":
		return true
	default:
		return false
	}
}
