package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/format"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/gopls"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
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

func NewXRefHandler(manager gopls.Manager) ToolHandler {
	if manager == nil {
		return missingManagerHandler()
	}
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		req, err := decodeParams[xrefParams](params)
		if err != nil {
			return nil, err
		}
		filePath, position, err := requireFilePosition(filePositionParams{
			FilePath: req.FilePath,
			Line:     req.Line,
			Column:   req.Column,
		})
		if err != nil {
			return nil, err
		}
		switch normalizeAction(req.Action) {
		case "references":
			return runReferences(ctx, manager, filePath, position, req)
		case "call_hierarchy":
			return runCallHierarchy(ctx, manager, filePath, position, req)
		case "type_hierarchy":
			return runTypeHierarchy(ctx, manager, filePath, position, req)
		default:
			return nil, fmt.Errorf("unsupported xref action %q", req.Action)
		}
	}
}

func runReferences(
	ctx context.Context,
	manager gopls.Manager,
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
	if verbosity == format.VerbosityFull {
		return format.NormalizeForDisplay(results), nil
	}
	return format.GroupLocationsByFile(results, total), nil
}

func runCallHierarchy(
	ctx context.Context,
	manager gopls.Manager,
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
	results = limitSlice(results, clampResultLimit(req.MaxResults, protocol.XRefResultLimit))
	if len(results) == 0 {
		return "no call hierarchy found", nil
	}
	return format.NormalizeForDisplay(results), nil
}

func runTypeHierarchy(
	ctx context.Context,
	manager gopls.Manager,
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
	results = limitSlice(results, clampResultLimit(req.MaxResults, protocol.XRefResultLimit))
	if len(results) == 0 {
		return "no type hierarchy found", nil
	}
	return format.NormalizeForDisplay(results), nil
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

var (
	_ = errors.New
	_ = json.RawMessage{}
)
