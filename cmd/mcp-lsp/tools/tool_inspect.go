package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

// filePositionParams 是需要 file:line:column 的 LSP 工具公共定位入参。
type filePositionParams struct {
	Pos        string `json:"pos"`
	LanguageID string `json:"language_id,omitempty"`
}

// inspectParams 是 inspect 工具的 action 加位置入参。
type inspectParams struct {
	Action string `json:"action"`
	filePositionParams
}

// NewInspectHandler 创建 inspect 工具处理器，按位置执行 hover/definition 等 LSP 查询。
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

// runHover 调用 LSP hover，并在无内容时返回标准空列表响应。
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

// runLocationInspect 执行 definition/implementation/type_definition 并补充函数范围。
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

// runSignatureHelp 查询调用点签名；无签名时返回可读文本而不是错误。
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

// limitSlice 按上限复制切片前缀，避免调用方误改原切片。
func limitSlice[T any](items []T, limit int) []T {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return append([]T(nil), items[:limit]...)
}

// hoverText 提取 hover 结果中的可显示文本。
func hoverText(result *protocol.HoverResult) string {
	if result == nil {
		return ""
	}
	return strings.TrimSpace(extractHoverValue(result.Contents))
}

// extractHoverValue 兼容不同 LSP server 的 hover 内容形状。
func extractHoverValue(value any) string {
	if text, ok := extractHoverDirectValue(value); ok {
		return text
	}
	if text, ok := extractHoverCollectionValue(value); ok {
		return text
	}
	return extractHoverFallbackValue(value)
}

// extractHoverDirectValue 提取 string 或 MarkupContent 形式的 hover 文本。
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

// extractHoverCollectionValue 提取数组或 map 形式的 hover 文本。
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

// extractHoverFallbackValue 通过 JSON 往返把未知 hover 结构转成通用形状。
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

// joinHoverParts 合并多段 hover 文本，空段会被跳过。
func joinHoverParts(items []any) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if text := extractHoverValue(item); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// extractHoverMapValue 从 map 中提取 value/language，并在有语言时渲染代码块。
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
