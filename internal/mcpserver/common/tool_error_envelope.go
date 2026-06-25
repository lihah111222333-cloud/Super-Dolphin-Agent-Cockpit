package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

// ToolErrorEnvelope is the machine-readable tool error payload returned from
// tools/call after a specific tool handler has been selected. JSON-RPC
// transport/protocol errors still use JSON-RPC error responses.
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

// appendErrorEnvelopeMeta cherry-picks meta keys that are immediately
// useful to the LLM. Unknown meta keys stay in the structuredContent
// JSON for callers that need them, but we don't echo every meta entry
// into the plain-text channel.
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

// ToolResultIsError 处理工具结果is错误。
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

// CodedToolError allows call sites and recovery code to pin a stable error
// code when string classification would be ambiguous.
type CodedToolError struct {
	Err       error
	Code      string
	Retryable bool
	Hint      string
	Meta      map[string]any
}

// Error 返回错误文本。
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

// NewCodedToolError 创建coded工具错误。
func NewCodedToolError(code string, err error, retryable bool, hint string) error {
	if err == nil {
		err = errors.New(strings.TrimSpace(code))
	}
	return &CodedToolError{Err: err, Code: strings.TrimSpace(code), Retryable: retryable, Hint: strings.TrimSpace(hint)}
}

// NewPanicToolError 创建panic工具错误。
func NewPanicToolError(recovered any) error {
	return &CodedToolError{
		Err:       fmt.Errorf("panic recovered: %v", recovered),
		Code:      "internal_panic",
		Retryable: false,
		Hint:      "The tool handler panicked; inspect logs and retry only after the bug is fixed.",
	}
}

// NewToolErrorEnvelope 创建工具错误包装。
func NewToolErrorEnvelope(toolName string, err error) ToolErrorEnvelope {
	return NewToolErrorEnvelopeWithMeta(toolName, "", err, nil)
}

// NewToolErrorEnvelopeWithMeta 创建带meta的工具错误包装。
func NewToolErrorEnvelopeWithMeta(toolName, languageID string, err error, extraMeta map[string]any) ToolErrorEnvelope {
	code, retryable, hint, codedMeta := ClassifyToolError(toolName, err)
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
	if err == nil {
		return "unknown", false, "next: inspect tool call arguments and retry with a concrete error", nil
	}
	var coded *CodedToolError
	if errors.As(err, &coded) && coded != nil {
		return firstNonEmptyString(coded.Code, "unknown"), coded.Retryable, coded.Hint, coded.Meta
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

type toolErrorClassifier struct {
	code      string
	retryable bool
	hint      func(toolName, message string) string
	match     func(error, string, string) bool
}

var toolErrorClassifiers = []toolErrorClassifier{
	{
		code: "patch_no_match",
		hint: staticToolHint("next: file action=read_file pos=<file>:<line> limit=<n>, then retry edit action=replace_range with literal patch context"),
		match: func(_ error, message string, toolName string) bool {
			return isEditTool(toolName) && (strings.Contains(message, "sequence not found") ||
				strings.Contains(message, "no candidate matched the patch context"))
		},
	},
	{
		code: "patch_ambiguous",
		hint: staticToolHint("next: edit action=replace_range patch=\"...\" with 1-2 extra space-prefixed context lines; inspect meta.candidate_locations"),
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
		hint: staticToolHint("next: choose a new dag_key or update the existing DAG with task_dag_apply_ops"),
		match: func(err error, _ string, toolName string) bool {
			return isTaskCreateDAGTool(toolName) && platformdb.IsConflict(err)
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
			if toolName == "edit" || toolName == "lsp_edit" {
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

func staticToolHint(value string) func(string, string) string {
	return func(string, string) string { return value }
}

func isTaskTool(toolName string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(toolName)), "task_")
}

func isLaunchAgentTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "launch_agent", "orchestration_launch_agent":
		return true
	default:
		return false
	}
}

func isEditTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "edit", "lsp_edit":
		return true
	default:
		return false
	}
}

func isTaskUpdateNodeTool(toolName string) bool {
	return strings.ToLower(strings.TrimSpace(toolName)) == "task_update_node"
}

func isTaskCreateDAGTool(toolName string) bool {
	return strings.ToLower(strings.TrimSpace(toolName)) == "task_create_dag"
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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
