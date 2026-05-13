package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type xrefParams struct {
	Action             string `json:"action"`
	FilePath           string `json:"file_path"`
	Line               int    `json:"line"`
	Column             int    `json:"column"`
	Direction          string `json:"direction"`
	IncludeDeclaration *bool  `json:"include_declaration"`
	Verbosity          string `json:"verbosity"`
	MaxResults         int    `json:"max_results"`
}

func NewXRefHandler(registry lspmanager.Registry) ToolHandler {
	return newManagerTool("lsp_xref", middleware.TierNormal, registry, decodeStrict, func(ctx context.Context, registry lspmanager.Registry, req xrefParams) (any, error) {
		manager, err := registry.GetManagerForFile(ctx, req.FilePath)
		if err != nil {
			return nil, err
		}
		filePath, position, err := resolveFilePositionRequest(ctx, filePositionParams{
			FilePath: req.FilePath,
			Line:     req.Line,
			Column:   req.Column,
		})
		if err != nil {
			return nil, err
		}
		return dispatchToolAction(ctx, "xref", req.Action, req, map[string]actionHandler[xrefParams]{
			"references": func(ctx context.Context, req xrefParams) (any, error) {
				return runReferences(ctx, manager, filePath, position, req)
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
) (any, error) {
	includeDeclaration := false
	if req.IncludeDeclaration != nil {
		includeDeclaration = *req.IncludeDeclaration
	}
	verbosity := format.NormalizeVerbosity(req.Verbosity)
	limit := format.ReferencesLimit(req.MaxResults, verbosity)
	results, err := manager.References(ctx, filePath, position, includeDeclaration)
	if err != nil {
		return nil, err
	}
	total := len(results)
	results = limitSlice(results, limit)
	if len(results) == 0 {
		return "no references found", nil
	}
	return renderByVerbosity(results, total, verbosity,
		func(items []protocol.LocationResult) any { return format.NormalizeForDisplay(items) },
		func(items []protocol.LocationResult, total int) any { return format.GroupLocationsByFile(items, total) },
	), nil
}

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
	results, err := manager.CallHierarchy(ctx, filePath, position, direction)
	if err != nil {
		return nil, err
	}
	return renderListResult(results, shared.ClampLimit(req.MaxResults, 1, protocol.XRefResultLimit, protocol.XRefResultLimit), "no call hierarchy found", func(items []protocol.CallHierarchyResult, _ int) any {
		return format.NormalizeForDisplay(items)
	})
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
	if err != nil {
		return nil, err
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
