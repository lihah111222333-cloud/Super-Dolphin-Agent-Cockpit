package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/format"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/gopls"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
)

type ToolHandler func(ctx context.Context, params json.RawMessage) (any, error)

type filePositionParams struct {
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
}

type inspectParams struct {
	Action string `json:"action"`
	filePositionParams
}

func NewInspectHandler(manager gopls.Manager) ToolHandler {
	if manager == nil {
		return missingManagerHandler()
	}
	return ToolHandler(wrapToolHandler("lsp_inspect", middleware.TierNormal, func(ctx context.Context, params json.RawMessage) (any, error) {
		req, err := decodeParams[inspectParams](params)
		if err != nil {
			return nil, err
		}
		filePath, position, err := requireFilePosition(req.filePositionParams)
		if err != nil {
			return nil, err
		}
		switch normalizeAction(req.Action) {
		case "hover":
			return runHover(ctx, manager, filePath, position)
		case "definition":
			return runLocationInspect(
				ctx,
				filePath,
				position,
				"no definition found",
				manager.Definition,
			)
		case "implementation":
			return runLocationInspect(
				ctx,
				filePath,
				position,
				"no implementation found",
				manager.Implementation,
			)
		case "type_definition":
			return runLocationInspect(
				ctx,
				filePath,
				position,
				"no type definition found",
				manager.TypeDefinition,
			)
		case "signature_help":
			return runSignatureHelp(ctx, manager, filePath, position)
		default:
			return nil, fmt.Errorf("unsupported inspect action %q", req.Action)
		}
	}))
}

func runHover(
	ctx context.Context,
	manager gopls.Manager,
	filePath string,
	position protocol.Position,
) (any, error) {
	result, err := manager.Hover(ctx, filePath, position)
	if err != nil {
		return nil, err
	}
	content := hoverText(result)
	if content == "" {
		return "no hover info available", nil
	}
	return content, nil
}

func runLocationInspect(
	ctx context.Context,
	filePath string,
	position protocol.Position,
	emptyMessage string,
	run func(context.Context, string, protocol.Position) ([]protocol.LocationResult, error),
) (any, error) {
	results, err := run(ctx, filePath, position)
	if err != nil {
		return nil, err
	}
	results = limitSlice(results, protocol.XRefResultLimit)
	if len(results) == 0 {
		return emptyMessage, nil
	}
	return format.NormalizeForDisplay(results), nil
}

func runSignatureHelp(
	ctx context.Context,
	manager gopls.Manager,
	filePath string,
	position protocol.Position,
) (any, error) {
	result, err := manager.SignatureHelp(ctx, filePath, position)
	if err != nil {
		return nil, err
	}
	if result == nil || len(result.Signatures) == 0 {
		return "no signature help found", nil
	}
	return result, nil
}

func missingManagerHandler() ToolHandler {
	return func(context.Context, json.RawMessage) (any, error) {
		return nil, errors.New("lsp manager is required")
	}
}

func decodeParams[T any](raw json.RawMessage) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode params: %w", err)
	}
	var extra struct{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return value, errors.New("decode params: unexpected trailing JSON payload")
	}
	return value, nil
}

func requireFilePosition(params filePositionParams) (string, protocol.Position, error) {
	filePath, err := requireFilePath(params.FilePath)
	if err != nil {
		return "", protocol.Position{}, err
	}
	position, err := requirePosition(params.Line, params.Column)
	if err != nil {
		return "", protocol.Position{}, err
	}
	return filePath, position, nil
}

func requireFilePath(raw string) (string, error) {
	filePath := strings.TrimSpace(raw)
	if filePath == "" {
		return "", errors.New("file_path is required")
	}
	return filePath, nil
}

func requirePosition(line, column int) (protocol.Position, error) {
	if line <= 0 {
		return protocol.Position{}, errors.New("line must be >= 1")
	}
	if column <= 0 {
		return protocol.Position{}, errors.New("column must be >= 1")
	}
	return protocol.Position{
		Line:      line - 1,
		Character: column - 1,
	}, nil
}

func normalizeAction(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func clampResultLimit(requested, fallback int) int {
	if requested <= 0 {
		return fallback
	}
	if requested > protocol.XRefResultLimit {
		return protocol.XRefResultLimit
	}
	return requested
}

func limitSlice[T any](items []T, limit int) []T {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return append([]T(nil), items[:limit]...)
}

func hoverText(result *protocol.HoverResult) string {
	if result == nil {
		return ""
	}
	return strings.TrimSpace(extractHoverValue(result.Contents))
}

func extractHoverValue(value any) string {
	if text, ok := extractHoverDirectValue(value); ok {
		return text
	}
	if text, ok := extractHoverCollectionValue(value); ok {
		return text
	}
	return extractHoverFallbackValue(value)
}

func extractHoverDirectValue(value any) (string, bool) {
	switch typed := value.(type) {
	case nil:
		return "", true
	case string:
		return strings.TrimSpace(typed), true
	case protocol.MarkupContent:
		return strings.TrimSpace(typed.Value), true
	case *protocol.MarkupContent:
		if typed == nil {
			return "", true
		}
		return strings.TrimSpace(typed.Value), true
	default:
		return "", false
	}
}

func extractHoverCollectionValue(value any) (string, bool) {
	switch typed := value.(type) {
	case []any:
		return joinHoverParts(typed), true
	case map[string]any:
		return extractHoverMapValue(typed), true
	default:
		return "", false
	}
}

func extractHoverFallbackValue(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return strings.TrimSpace(fmt.Sprint(value))
	}
	var generic any
	if err := json.Unmarshal(payload, &generic); err != nil {
		return strings.TrimSpace(string(payload))
	}
	return extractHoverValue(generic)
}

func joinHoverParts(items []any) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if text := extractHoverValue(item); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func extractHoverMapValue(value map[string]any) string {
	raw, _ := value["value"].(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if language, ok := value["language"].(string); ok {
		language = strings.TrimSpace(language)
		if language != "" {
			return fmt.Sprintf("```%s\n%s\n```", language, raw)
		}
	}
	return raw
}
