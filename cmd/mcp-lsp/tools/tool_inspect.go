package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
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

type hoverLineResult struct {
	format string
	text   string
	hint   string
}

// ToPlainText 把 hover 内容封装为可逆单行记录，动态文本不能注入协议行。
func (result hoverLineResult) ToPlainText() string {
	total := 0
	if result.text != "" {
		total = 1
	}
	lines := []string{lineprotocol.HeaderLine(total, total, false, "hover")}
	if total == 1 {
		lines = append(lines, lineprotocol.FieldsRecord("ROW",
			lineprotocol.Field{Key: "format", Value: result.format},
			lineprotocol.Field{Key: "text", Value: result.text},
		))
	}
	if result.hint != "" {
		lines = append(lines, lineprotocol.TextRecord("HINT", result.hint))
	}
	return strings.Join(lines, "\n")
}

type signatureLineResult struct {
	result *protocol.SignatureHelpResult
	hint   string
}

// ToPlainText 把签名和参数扁平为固定字段顺序的 ROW 记录。
func (result signatureLineResult) ToPlainText() string {
	total := signatureFlatRowCount(result.result)
	lines := []string{lineprotocol.HeaderLine(total, total, false, "signature")}
	if result.result != nil {
		lines = appendSignatureRows(lines, result.result)
	}
	if result.hint != "" {
		lines = append(lines, lineprotocol.TextRecord("HINT", result.hint))
	}
	return strings.Join(lines, "\n")
}

// NewInspectHandler 创建 inspect 工具处理器，按位置执行 hover/definition 等 LSP 查询。
func NewInspectHandler(registry lspmanager.Registry) ToolHandler {
	return newManagerToolWithoutOuterTimeout("inspect", middleware.TierNormal, registry, decodeLenient, func(ctx context.Context, registry lspmanager.Registry, req inspectParams) (any, error) {
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
		return hoverLineResult{hint: rustDetachedWorkspaceMessage(filePath, "hover", "no hover info available")}, nil
	}
	return hoverLineResult{format: hoverContentFormat(result.Contents), text: content}, nil
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
		return signatureLineResult{hint: "move-cursor-inside-call-arguments-or-after-comma"}, nil
	}
	return signatureLineResult{result: result}, nil
}

func signatureFlatRowCount(result *protocol.SignatureHelpResult) int {
	if result == nil {
		return 0
	}
	total := len(result.Signatures)
	for _, signature := range result.Signatures {
		total += len(signature.Parameters)
	}
	return total
}

func appendSignatureRows(lines []string, result *protocol.SignatureHelpResult) []string {
	for signatureIndex, signature := range result.Signatures {
		lines = append(lines, renderSignatureRow(result, signatureIndex, signature))
		lines = appendParameterRows(lines, result, signatureIndex, signature.Parameters)
	}
	return lines
}

func renderSignatureRow(result *protocol.SignatureHelpResult, index int, signature protocol.SignatureInformationResult) string {
	documentation, documentationFormat := protocolTextAndFormat(signature.Documentation)
	return lineprotocol.FieldsRecord("ROW",
		lineprotocol.Field{Key: "row_kind", Value: "signature"},
		lineprotocol.Field{Key: "signature_index", Value: strconv.Itoa(index)},
		lineprotocol.Field{Key: "label", Value: signature.Label},
		lineprotocol.Field{Key: "documentation", Value: documentation},
		lineprotocol.Field{Key: "documentation_format", Value: documentationFormat},
		lineprotocol.Field{Key: "active", Value: strconv.Itoa(boolToInt(signatureIsActive(result, index)))},
		lineprotocol.Field{Key: "active_parameter", Value: activeParameterIndex(result, index)},
	)
}

func appendParameterRows(
	lines []string,
	result *protocol.SignatureHelpResult,
	signatureIndex int,
	parameters []protocol.ParameterInformationResult,
) []string {
	for parameterIndex, parameter := range parameters {
		lines = append(lines, renderParameterRow(result, signatureIndex, parameterIndex, parameter))
	}
	return lines
}

func renderParameterRow(
	result *protocol.SignatureHelpResult,
	signatureIndex int,
	parameterIndex int,
	parameter protocol.ParameterInformationResult,
) string {
	documentation, documentationFormat := protocolTextAndFormat(parameter.Documentation)
	return lineprotocol.FieldsRecord("ROW",
		lineprotocol.Field{Key: "row_kind", Value: "parameter"},
		lineprotocol.Field{Key: "signature_index", Value: strconv.Itoa(signatureIndex)},
		lineprotocol.Field{Key: "parameter_index", Value: strconv.Itoa(parameterIndex)},
		lineprotocol.Field{Key: "label", Value: parameter.Label},
		lineprotocol.Field{Key: "label_offsets", Value: intListText(parameter.LabelOffsets)},
		lineprotocol.Field{Key: "documentation", Value: documentation},
		lineprotocol.Field{Key: "documentation_format", Value: documentationFormat},
		lineprotocol.Field{Key: "active", Value: strconv.Itoa(boolToInt(parameterIsActive(result, signatureIndex, parameterIndex)))},
	)
}

func signatureIsActive(result *protocol.SignatureHelpResult, index int) bool {
	return result != nil && result.ActiveSignature != nil && *result.ActiveSignature == index
}

func parameterIsActive(result *protocol.SignatureHelpResult, signatureIndex, parameterIndex int) bool {
	return signatureIsActive(result, signatureIndex) && result.ActiveParameter != nil && *result.ActiveParameter == parameterIndex
}

func activeParameterIndex(result *protocol.SignatureHelpResult, signatureIndex int) string {
	if !signatureIsActive(result, signatureIndex) || result.ActiveParameter == nil {
		return "-1"
	}
	return strconv.Itoa(*result.ActiveParameter)
}

func intListText(values []int) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, ",")
}

func protocolTextAndFormat(value any) (string, string) {
	return extractHoverValue(value), hoverContentFormat(value)
}

func hoverContentFormat(value any) string {
	switch typed := value.(type) {
	case string:
		return "plaintext"
	case protocol.MarkupContent:
		return normalizedContentFormat(typed.Kind)
	case *protocol.MarkupContent:
		if typed != nil {
			return normalizedContentFormat(typed.Kind)
		}
	case map[string]any:
		if kind, ok := typed["kind"].(string); ok {
			return normalizedContentFormat(kind)
		}
	case []any:
		return "mixed"
	}
	return "unknown"
}

func normalizedContentFormat(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "markdown" || value == "plaintext" {
		return value
	}
	return "unknown"
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
