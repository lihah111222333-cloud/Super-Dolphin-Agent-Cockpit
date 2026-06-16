package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/format"
	lspmanager "github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/protocol"
)

type filePositionParams struct {
	Pos        string `json:"pos"`
	LanguageID string `json:"language_id,omitempty"`
}

type inspectParams struct {
	Action string `json:"action"`
	filePositionParams
}

// NewInspectHandler 创建inspect处理器。
func NewInspectHandler(registry lspmanager.Registry) ToolHandler {
	return newManagerTool("inspect", middleware.TierNormal, registry, decodeLenient, func(ctx context.Context, registry lspmanager.Registry, req inspectParams) (any, error) {
		filePath, position, err := resolveFilePositionRequest(ctx, req.filePositionParams)
		if err != nil {
			return nil, err
		}
		manager, err := managerForFile(ctx, registry, filePath, req.LanguageID)
		if err != nil {
			return nil, err
		}
		funcEnricher := newFuncRangeEnricher(ctx, registry)
		return dispatchToolAction(ctx, "inspect", req.Action, req, map[string]actionHandler[inspectParams]{
			"hover": func(ctx context.Context, _ inspectParams) (any, error) {
				return runHover(ctx, manager, filePath, position)
			},
			"definition": func(ctx context.Context, _ inspectParams) (any, error) {
				return runLocationInspect(ctx, filePath, position, "definition", "no definition found", manager.Definition, funcEnricher)
			},
			"implementation": func(ctx context.Context, _ inspectParams) (any, error) {
				return runLocationInspect(ctx, filePath, position, "implementation", "no implementation found", manager.Implementation, funcEnricher)
			},
			"type_definition": func(ctx context.Context, _ inspectParams) (any, error) {
				return runLocationInspect(ctx, filePath, position, "type definition", "no type definition found", manager.TypeDefinition, funcEnricher)
			},
			"signature_help": func(ctx context.Context, _ inspectParams) (any, error) {
				return runSignatureHelp(ctx, manager, filePath, position)
			},
		})
	})
}

func runHover(
	ctx context.Context,
	manager lspmanager.Manager,
	filePath string,
	position protocol.Position,
) (any, error) {
	result, err := manager.Hover(ctx, filePath, position)
	if err != nil {
		return nil, err
	}
	content := hoverText(result)
	if content == "" {
		return emptyListEnvelope{
			Success: true,
			Data:    []any{},
			Meta:    resultMeta{Count: 0, Message: rustDetachedWorkspaceMessage(filePath, "hover", "no hover info available")},
		}, nil
	}
	return content, nil
}

func runLocationInspect(
	ctx context.Context,
	filePath string,
	position protocol.Position,
	capability string,
	emptyMessage string,
	run func(context.Context, string, protocol.Position) ([]protocol.LocationResult, error),
	enricher format.SymbolProvider,
) (any, error) {
	results, err := run(ctx, filePath, position)
	if isUnsupportedCapability(err) {
		return unsupportedCapabilityEmptyResult(capability), nil
	}
	if err != nil {
		if capability == "implementation" {
			return nil, implementationTargetError(err)
		}
		return nil, err
	}
	format.EnrichLocationResultsWithFuncRange(results, enricher)
	total := len(results)
	grouped := groupLocationsByFile(ctx, limitSlice(results, protocol.XRefResultLimit), total)
	if total == 0 {
		grouped.Hint = emptyMessage
	}
	return grouped, nil
}

func runSignatureHelp(
	ctx context.Context,
	manager lspmanager.Manager,
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

// extractHoverDirectValue 提取悬停direct值。
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
