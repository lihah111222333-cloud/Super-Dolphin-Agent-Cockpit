package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// ToolErrorEnvelope 是工具 handler 已选定后由 tools/call 返回的机器可读错误载荷。
// JSON-RPC transport/protocol 层错误仍使用 JSON-RPC error response，不走这个 envelope。
type ToolErrorEnvelope struct {
	Success   bool           `json:"success"`
	Error     string         `json:"error"`
	Code      string         `json:"code,omitempty"`
	Retryable bool           `json:"retryable,omitempty"`
	Hint      string         `json:"hint,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}

// ToPlainText renders the envelope as LLM-readable text instead of leaving
// callers to interpret raw JSON. The format groups the most actionable
// signals (error → hint → retry) at the top and surfaces useful meta
// fields like suggested_columns / line_text without dumping every key.
// ToPlainText 渲染为纯文本。
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

// appendErrorEnvelopeMeta 只挑选 LLM 立即可用的 meta 字段写入纯文本。
// 未识别字段仍留在 structuredContent，避免把全部结构化数据重复灌进文本通道。
func appendErrorEnvelopeMeta(sb *strings.Builder, meta map[string]any) {
	if len(meta) == 0 {
		return
	}
	appendErrorEnvelopeLineMeta(sb, meta)
	appendSuggestedColumnsMeta(sb, meta["suggested_columns"])
}

// appendErrorEnvelopeLineMeta 输出定位类 meta，帮助调用方直接跳到问题行。
func appendErrorEnvelopeLineMeta(sb *strings.Builder, meta map[string]any) {
	if line, ok := meta["line"].(int); ok {
		fmt.Fprintf(sb, "Line: %d\n", line)
	}
	if lineText, ok := meta["line_text"].(string); ok && lineText != "" {
		fmt.Fprintf(sb, "Line text: %s\n", lineText)
	}
}

// appendSuggestedColumnsMeta 输出 parser/patch 工具给出的列建议，兼容两种 slice 形态。
func appendSuggestedColumnsMeta(sb *strings.Builder, raw any) {
	switch cols := raw.(type) {
	case []map[string]any:
		appendSuggestedColumnMaps(sb, cols)
	case []any:
		appendSuggestedColumnAnyMaps(sb, cols)
	}
}

// appendSuggestedColumnMaps 渲染已经解码成 map 的 suggested_columns。
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

// appendSuggestedColumnAnyMaps 渲染 JSON 解码后仍为 []any 的 suggested_columns。
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

// suggestedColumnValues 从 suggested_columns 条目中提取标识符和列号。
func suggestedColumnValues(item map[string]any) (string, int) {
	ident, _ := item["identifier"].(string)
	col, _ := numericMetaInt(item["column"])
	return ident, col
}

// numericMetaInt 将 JSON number 或已解码整数转为 int，其他类型返回 false。
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
// 明确的 ToolErrorEnvelope 优先；普通对象只在 success=false 时视为错误。
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

// CodedToolError 允许调用点或 recover 路径显式固定错误 code。
// 当字符串分类容易歧义时，用它保留 retryable、hint 和 meta 等稳定信号。
type CodedToolError struct {
	Err       error
	Code      string
	Retryable bool
	Hint      string
	Meta      map[string]any
}

// Error 返回底层错误文本，nil receiver 或 nil Err 返回空字符串。
func (e *CodedToolError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

// Unwrap 返回底层错误。
func (e *CodedToolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NewCodedToolError 创建带稳定 code/hint 的工具错误，err 为空时使用 code 作为错误文本。
func NewCodedToolError(code string, err error, retryable bool, hint string) error {
	if err == nil {
		err = errors.New(strings.TrimSpace(code))
	}
	return &CodedToolError{Err: err, Code: strings.TrimSpace(code), Retryable: retryable, Hint: strings.TrimSpace(hint)}
}

// NewPanicToolError 将工具 handler panic 转为不可重试的内部错误。
func NewPanicToolError(recovered any) error {
	return &CodedToolError{
		Err:       fmt.Errorf("panic recovered: %v", recovered),
		Code:      "internal_panic",
		Retryable: false,
		Hint:      "The tool handler panicked; inspect logs and retry only after the bug is fixed.",
	}
}

// ToolErrorClassification 是调用方拥有的工具错误分类结果。
// sidecar 可在本地识别领域错误，而不让 common 依赖具体持久化或运行时包。
type ToolErrorClassification struct {
	Code      string
	Retryable bool
	Hint      string
	Meta      map[string]any
}

// ToolErrorClassifier 在调用方能识别工具错误时返回分类；false 表示回落到 common 默认规则。
type ToolErrorClassifier func(toolName string, err error) (ToolErrorClassification, bool)

// NewToolErrorEnvelope 使用默认 meta 创建工具错误 envelope。
func NewToolErrorEnvelope(toolName string, err error) ToolErrorEnvelope {
	return NewToolErrorEnvelopeWithMeta(toolName, "", err, nil)
}

// NewToolErrorEnvelopeWithMeta 分类错误并合并语言、分类器和调用方传入的 meta。
func NewToolErrorEnvelopeWithMeta(toolName, languageID string, err error, extraMeta map[string]any) ToolErrorEnvelope {
	return NewToolErrorEnvelopeWithClassifier(toolName, languageID, err, extraMeta, nil)
}

// NewToolErrorEnvelopeWithClassifier 先应用调用方分类器，再回落到 common 默认分类。
// CodedToolError 仍是最高优先级，避免显式错误码被侧向规则覆盖。
func NewToolErrorEnvelopeWithClassifier(toolName, languageID string, err error, extraMeta map[string]any, classifier ToolErrorClassifier) ToolErrorEnvelope {
	code, retryable, hint, codedMeta := ClassifyToolErrorWithClassifier(toolName, err, classifier)
	meta := map[string]any{"tool": strings.TrimSpace(toolName)}
	if languageID = normalizeEnvelopeLanguageID(languageID); languageID != "" {
		meta["language_id"] = languageID
	}
	for k, v := range codedMeta {
		if strings.TrimSpace(k) != "" {
			meta[k] = v
		}
	}
	for k, v := range extraMeta {
		if strings.TrimSpace(k) != "" {
			meta[k] = v
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

// ClassifyToolError 分类工具错误。
func ClassifyToolError(toolName string, err error) (code string, retryable bool, hint string, meta map[string]any) {
	return ClassifyToolErrorWithClassifier(toolName, err, nil)
}

// ClassifyToolErrorWithClassifier 分类工具错误，并允许调用方注入 sidecar 局部规则。
func ClassifyToolErrorWithClassifier(toolName string, err error, classifier ToolErrorClassifier) (code string, retryable bool, hint string, meta map[string]any) {
	if err == nil {
		return "unknown", false, "next: inspect tool call arguments and retry with a concrete error", nil
	}
	var coded *CodedToolError
	if errors.As(err, &coded) && coded != nil {
		return firstNonEmptyString(coded.Code, "unknown"), coded.Retryable, coded.Hint, coded.Meta
	}
	if classifier != nil {
		if classification, ok := classifier(toolName, err); ok {
			return firstNonEmptyString(classification.Code, "tool_error"), classification.Retryable, classification.Hint, classification.Meta
		}
	}
	message := strings.ToLower(err.Error())
	normalizedTool := strings.ToLower(strings.TrimSpace(toolName))
	for _, classifier := range toolErrorClassifiers {
		if classifier.match(err, message, normalizedTool) {
			return classifier.code, classifier.retryable, classifier.hint(normalizedTool, message), nil
		}
	}
	return "tool_error", false, "next: inspect the tool error message and logs, then retry after fixing the reported issue", nil
}

// toolErrorClassifier 描述一条工具错误分类规则，按顺序匹配第一条命中项。
type toolErrorClassifier struct {
	code      string
	retryable bool
	hint      func(toolName, message string) string
	match     func(error, string, string) bool
}

// toolErrorClassifiers 按优先级排列工具错误分类规则，具体工具错误必须先于通用错误。
var toolErrorClassifiers = []toolErrorClassifier{
	{
		code: "patch_no_match",
		hint: staticToolHint("next: file action=read_file pos=<file>:<line> limit=<n>, then retry patch_edit action=replace_range with literal patch context"),
		match: func(_ error, message string, toolName string) bool {
			return isEditTool(toolName) && (strings.Contains(message, "sequence not found") ||
				strings.Contains(message, "no candidate matched the patch context"))
		},
	},
	{
		code: "patch_ambiguous",
		hint: staticToolHint("next: patch_edit action=replace_range patch=\"...\" with 1-2 extra space-prefixed context lines; inspect meta.candidate_locations"),
		match: func(_ error, message string, toolName string) bool {
			if !isEditTool(toolName) {
				return false
			}
			return strings.Contains(message, "ambiguous match") ||
				strings.Contains(message, "multiple candidates matched the patch context")
		},
	},
	{
		code:  "database_schema_missing",
		hint:  staticToolHint("next: run database migrations or verify the embedded database schema"),
		match: func(_ error, message string, _ string) bool { return isDatabaseSchemaMissingMessage(message) },
	},
	{
		code: "internal_panic",
		hint: staticToolHint("next: inspect logs and retry only after fixing the tool handler bug"),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "panic recovered")
		},
	},
	{
		code: "capability_unsupported",
		hint: staticToolHint("next: use a supported tool/action/language for this helper or language adapter"),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "unsupported run language") ||
				strings.Contains(message, "unsupported helper language") ||
				(strings.Contains(message, "unsupported") && strings.Contains(message, "capability"))
		},
	},
	{
		code: "language_unsupported",
		hint: staticToolHint("next: choose file_path or language_id with a registered language adapter"),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "unsupported language") ||
				strings.Contains(message, "unsupported language adapter") ||
				strings.Contains(message, "unsupported language for lsp toolchain")
		},
	},
	{
		code: "schema_invalid",
		hint: staticToolHint("next: check the tool schema and retry with documented field names and JSON value types"),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "decode params") ||
				strings.Contains(message, "decode ") ||
				strings.Contains(message, "unknown field") ||
				strings.Contains(message, "json:")
		},
	},
	{
		code: "cwd_required",
		hint: staticToolHint("next: pass a non-empty cwd or parent_id for an existing parent agent with cwd"),
		match: func(err error, message string, toolName string) bool {
			return isLaunchAgentTool(toolName) && (errors.Is(err, contract.ErrLaunchCWDRequired) ||
				strings.Contains(message, "launch_agent cwd is required") ||
				strings.Contains(message, "thread start cwd is required"))
		},
	},
	{
		code: "cwd_invalid",
		hint: staticToolHint("next: pass an explicit absolute cwd path; dot and relative cwd are not accepted"),
		match: func(err error, message string, toolName string) bool {
			return isLaunchAgentTool(toolName) && (errors.Is(err, contract.ErrLaunchCWDInvalid) ||
				strings.Contains(message, "cwd must be explicit") ||
				strings.Contains(message, "cwd must be an absolute path"))
		},
	},
	{
		code: "provider_required",
		hint: staticToolHint("next: pass provider=codex|claude or omit provider for launch_agent default"),
		match: func(_ error, message string, toolName string) bool {
			return isLaunchAgentTool(toolName) && strings.Contains(message, "provider is required")
		},
	},
	{
		code: "provider_invalid",
		hint: staticToolHint("next: pass provider=codex|claude"),
		match: func(_ error, message string, toolName string) bool {
			return isLaunchAgentTool(toolName) && strings.Contains(message, "invalid provider")
		},
	},
	{
		code: "launch_request_invalid",
		hint: staticToolHint("next: fix launch_agent arguments and retry with non-empty required fields and supported enum values"),
		match: func(_ error, message string, toolName string) bool {
			return isLaunchAgentTool(toolName) && isLaunchRequestInvalidMessage(message)
		},
	},
	{
		code: "invalid_input",
		hint: staticToolHint("next: fix the task DAG request, node status, or transition inputs"),
		match: func(_ error, message string, toolName string) bool {
			return isTaskTool(toolName) && (strings.Contains(message, "apply_ops invalid request") ||
				strings.Contains(message, "invalid task") ||
				strings.Contains(message, "invalid request") ||
				strings.Contains(message, "validate transition") ||
				(isTaskUpdateNodeTool(toolName) && strings.HasPrefix(strings.TrimSpace(message), "transition:")))
		},
	},
	{
		code:      "lsp_unavailable",
		retryable: true,
		hint:      staticToolHint("next: retry after language server startup or inspect manager diagnostics"),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "language server is starting") ||
				(strings.Contains(message, "language server") && (strings.Contains(message, "not ready") || strings.Contains(message, "unavailable"))) ||
				(strings.Contains(message, "lsp") && (strings.Contains(message, "not ready") || strings.Contains(message, "unavailable")))
		},
	},
	{
		code: "dependency_missing",
		hint: staticToolHint("next: install ast-grep or ensure sg is available in PATH"),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "sg not found in path")
		},
	},
	{
		code: "identifier_not_found",
		hint: staticToolHint("Move the pos column onto a function, type, variable, or other identifier before retrying."),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "identifier not found") ||
				strings.Contains(message, "no identifier found")
		},
	},
	{
		code: "file_not_found",
		hint: staticToolHint("next: verify file_path is under the trusted workspace and exists on disk"),
		match: func(err error, message string, _ string) bool {
			return errors.Is(err, os.ErrNotExist) ||
				strings.Contains(message, "not found") ||
				strings.Contains(message, "no such file") ||
				strings.Contains(message, "no rows in result set")
		},
	},
	{
		code: "path_invalid",
		hint: staticToolHint("next: pass a regular file_path for file actions; directories are not valid file_path values"),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "must reference a regular file") ||
				strings.Contains(message, "is not a regular file") ||
				strings.Contains(message, "must be a regular file")
		},
	},
	{
		code: "path_outside_workspace",
		hint: staticToolHint("next: use a path under trusted workspace roots or add the directory to workspace roots"),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "outside workspace roots") ||
				strings.Contains(message, "outside allowed workspace roots")
		},
	},
	{
		code:      "lsp_timeout",
		retryable: true,
		hint:      staticToolHint("next: narrow query/path/glob or reduce max_results after the language server finishes indexing"),
		match: func(err error, message string, _ string) bool {
			return errors.Is(err, context.DeadlineExceeded) ||
				strings.Contains(message, "timeout") ||
				strings.Contains(message, "deadline")
		},
	},
	{
		code: "position_invalid",
		hint: func(toolName, _ string) string {
			if toolName == "patch_edit" {
				return "next: use 1-based line/column inputs; for replace_range coordinate errors, prefer patch or edits"
			}
			return "next: use 1-based line and column inputs with the cursor on an identifier"
		},
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "line must") ||
				strings.Contains(message, "column must") ||
				strings.Contains(message, "line is out of range") ||
				strings.Contains(message, "column is out of range") ||
				strings.Contains(message, "end_line") ||
				strings.Contains(message, "end position") ||
				strings.Contains(message, "position must")
		},
	},
	{
		code:      "lsp_client_closed",
		retryable: true,
		hint:      staticToolHint("next: retry once so the manager can recreate the language server client"),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "client") && strings.Contains(message, "closed")
		},
	},
	{
		code: "scope_ambiguous",
		hint: staticToolHint("next: provide exactly one unambiguous scope selector such as file_path or language"),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "ambiguous") ||
				strings.Contains(message, "exactly one of") ||
				strings.Contains(message, "could not resolve scope")
		},
	},
}

// isDatabaseSchemaMissingMessage 判断错误消息是否属于数据库 schema 缺失。
func isDatabaseSchemaMissingMessage(message string) bool {
	return (strings.Contains(message, "relation ") && strings.Contains(message, "does not exist")) ||
		(strings.Contains(message, "column ") && strings.Contains(message, "does not exist")) ||
		strings.Contains(message, "no such table") ||
		strings.Contains(message, "missing database schema") ||
		strings.Contains(message, "sqlstate 42p01") ||
		strings.Contains(message, "sqlstate 42703")
}

// isLaunchRequestInvalidMessage 判断错误消息是否属于 launch 请求参数无效。
func isLaunchRequestInvalidMessage(message string) bool {
	return strings.Contains(message, " is required") ||
		(strings.Contains(message, "context_mode") && strings.Contains(message, "requires")) ||
		strings.Contains(message, "invalid memory_scope") ||
		strings.Contains(message, "invalid agent") ||
		strings.Contains(message, "must be project, user, or local")
}

// staticToolHint 将固定提示包装成 classifier 所需的函数签名。
func staticToolHint(value string) func(string, string) string {
	return func(string, string) string { return value }
}

// isTaskTool 判断工具名是否属于 task_* orchestration 工具族。
func isTaskTool(toolName string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(toolName)), "task_")
}

// isLaunchAgentTool 判断工具名是否是启动 agent 的入口。
func isLaunchAgentTool(toolName string) bool {
	return contract.IsOrchestrationLaunchTool(toolName)
}

// isEditTool 判断工具名是否是 LSP patch_edit。
func isEditTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "patch_edit":
		return true
	default:
		return false
	}
}

// isTaskUpdateNodeTool 判断工具名是否为 task_update_node。
func isTaskUpdateNodeTool(toolName string) bool {
	return strings.ToLower(strings.TrimSpace(toolName)) == "task_update_node"
}

// firstNonEmptyString 返回首个非空字符串，用于 coded error 字段兜齐。
func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// errorText 安全读取 error 文本，nil error 返回空字符串。
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// normalizeEnvelopeLanguageID 规范化 envelope 里的 language_id，便于前端分组展示。
func normalizeEnvelopeLanguageID(languageID string) string {
	return strings.ToLower(strings.TrimSpace(languageID))
}
