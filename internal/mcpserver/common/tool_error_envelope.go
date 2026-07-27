package common

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/mcpwire"
)

// ToolErrorEnvelope 是工具 handler 已选定后由 tools/call 返回的机器可读错误载荷。
type ToolErrorEnvelope struct {
	Success   bool           `json:"success"`
	Error     string         `json:"error"`
	Code      string         `json:"code,omitempty"`
	Retryable bool           `json:"retryable,omitempty"`
	Hint      string         `json:"hint,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}

// ToPlainText 将错误 envelope 渲染为模型可读文本。
func (e ToolErrorEnvelope) ToPlainText() string {
	var sb strings.Builder
	header := "Tool error"
	if tool, ok := e.Meta["tool"].(string); ok && strings.TrimSpace(tool) != "" {
		header = fmt.Sprintf("Tool error in %q", strings.TrimSpace(tool))
	}
	if e.Code != "" {
		fmt.Fprintf(&sb, "%s [%s]: %s\n", header, e.Code, strings.TrimSpace(e.Error))
	} else {
		fmt.Fprintf(&sb, "%s: %s\n", header, strings.TrimSpace(e.Error))
	}
	if hint := strings.TrimSpace(e.Hint); hint != "" {
		fmt.Fprintf(&sb, "Hint: %s\n", hint)
	}
	if e.Retryable {
		sb.WriteString("Retryable: yes\n")
	}
	appendErrorEnvelopeMeta(&sb, e.Meta)
	return strings.TrimSpace(sb.String())
}

func appendErrorEnvelopeMeta(sb *strings.Builder, meta map[string]any) {
	if len(meta) == 0 {
		return
	}
	appendErrorEnvelopeLineMeta(sb, meta)
	appendSuggestedColumnsMeta(sb, meta["suggested_columns"])
}

func appendErrorEnvelopeLineMeta(sb *strings.Builder, meta map[string]any) {
	if line, ok := meta["line"].(int); ok {
		fmt.Fprintf(sb, "Line: %d\n", line)
	}
	if lineText, ok := meta["line_text"].(string); ok && lineText != "" {
		fmt.Fprintf(sb, "Line text: %s\n", lineText)
	}
}

func appendSuggestedColumnsMeta(sb *strings.Builder, raw any) {
	switch cols := raw.(type) {
	case []map[string]any:
		appendSuggestedColumnMaps(sb, cols)
	case []any:
		appendSuggestedColumnAnyMaps(sb, cols)
	}
}

func appendSuggestedColumnMaps(sb *strings.Builder, cols []map[string]any) {
	if len(cols) == 0 {
		return
	}
	sb.WriteString("Suggested columns:\n")
	for _, item := range cols {
		ident, col := suggestedColumnValues(item)
		fmt.Fprintf(sb, "  - %q at column %d\n", ident, col)
	}
}

func appendSuggestedColumnAnyMaps(sb *strings.Builder, cols []any) {
	if len(cols) == 0 {
		return
	}
	wroteHeader := false
	for _, raw := range cols {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if !wroteHeader {
			wroteHeader = true
			sb.WriteString("Suggested columns:\n")
		}
		ident, col := suggestedColumnValues(item)
		fmt.Fprintf(sb, "  - %q at column %d\n", ident, col)
	}
}

func suggestedColumnValues(item map[string]any) (string, int) {
	ident, _ := item["identifier"].(string)
	col, _ := numericMetaInt(item["column"])
	return ident, col
}

func numericMetaInt(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

// ToolResultIsError 判断工具结果是否应标记为 MCP isError。
func ToolResultIsError(value any) bool {
	switch envelope := value.(type) {
	case ToolErrorEnvelope:
		return !envelope.Success
	case *ToolErrorEnvelope:
		return envelope == nil || !envelope.Success
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return true
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed[0] != '{' {
		return false
	}
	var marker struct {
		Success *bool `json:"success"`
	}
	if err := json.Unmarshal(raw, &marker); err != nil {
		return true
	}
	return marker.Success != nil && !*marker.Success
}

// CodedToolError 复用中立 mcpwire 包的稳定编码错误。
type CodedToolError = mcpwire.CodedToolError

// ToolErrorClassification 复用中立 mcpwire 包的错误分类结果。
type ToolErrorClassification = mcpwire.ToolErrorClassification

// ToolErrorClassifier 复用中立 mcpwire 包的调用方分类函数。
type ToolErrorClassifier = mcpwire.ToolErrorClassifier

// NewCodedToolError 创建带稳定错误码、重试语义和修复提示的工具错误。
func NewCodedToolError(code string, err error, retryable bool, hint string) error {
	return mcpwire.NewCodedToolError(code, err, retryable, hint)
}

// NewPanicToolError 将工具 handler panic 转换为不可重试的内部错误。
func NewPanicToolError(recovered any) error {
	return mcpwire.NewPanicToolError(recovered)
}

// NewToolErrorEnvelope 将工具错误转换为不含额外元数据的 MCP 错误载荷。
func NewToolErrorEnvelope(toolName string, err error) ToolErrorEnvelope {
	return NewToolErrorEnvelopeWithMeta(toolName, "", err, nil)
}

// NewToolErrorEnvelopeWithMeta 将语言和调用方元数据合并到 MCP 错误载荷。
func NewToolErrorEnvelopeWithMeta(toolName, languageID string, err error, extraMeta map[string]any) ToolErrorEnvelope {
	return NewToolErrorEnvelopeWithClassifier(toolName, languageID, err, extraMeta, nil)
}

// NewToolErrorEnvelopeWithClassifier 使用注入的领域分类器构造稳定 MCP 错误载荷。
func NewToolErrorEnvelopeWithClassifier(toolName, languageID string, err error, extraMeta map[string]any, classifier ToolErrorClassifier) ToolErrorEnvelope {
	code, retryable, hint, codedMeta := mcpwire.ClassifyToolErrorWithClassifier(toolName, err, classifier)
	meta := map[string]any{"tool": strings.TrimSpace(toolName)}
	if languageID = normalizeEnvelopeLanguageID(languageID); languageID != "" {
		meta["language_id"] = languageID
	}
	for key, value := range codedMeta {
		if strings.TrimSpace(key) != "" {
			meta[key] = value
		}
	}
	for key, value := range extraMeta {
		if strings.TrimSpace(key) != "" {
			meta[key] = value
		}
	}
	return ToolErrorEnvelope{
		Success:   false,
		Error:     errorText(err),
		Code:      code,
		Retryable: retryable,
		Hint:      hint,
		Meta:      meta,
	}
}

// ClassifyToolError 使用中立默认规则分类工具错误。
func ClassifyToolError(toolName string, err error) (code string, retryable bool, hint string, meta map[string]any) {
	return mcpwire.ClassifyToolError(toolName, err)
}

// ClassifyToolErrorWithClassifier 先应用显式领域分类器，再回退到中立默认规则。
func ClassifyToolErrorWithClassifier(toolName string, err error, classifier ToolErrorClassifier) (code string, retryable bool, hint string, meta map[string]any) {
	return mcpwire.ClassifyToolErrorWithClassifier(toolName, err, classifier)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func normalizeEnvelopeLanguageID(languageID string) string {
	return strings.ToLower(strings.TrimSpace(languageID))
}
