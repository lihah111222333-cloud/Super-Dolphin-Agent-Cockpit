package mcpwire

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// CodedToolError 允许调用点显式固定错误 code、retryable、hint 和 meta。
type CodedToolError struct {
	Err       error
	Code      string
	Retryable bool
	Hint      string
	Meta      map[string]any
}

// Error 返回被编码工具错误的原始消息；空接收者安全返回空串。
func (e *CodedToolError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

// Unwrap 暴露原始错误，供 errors.Is 与 errors.As 保留错误链。
func (e *CodedToolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NewCodedToolError 创建带稳定 code/hint 的工具错误。
func NewCodedToolError(code string, err error, retryable bool, hint string) error {
	if err == nil {
		err = errors.New(strings.TrimSpace(code))
	}
	return &CodedToolError{
		Err:       err,
		Code:      strings.TrimSpace(code),
		Retryable: retryable,
		Hint:      strings.TrimSpace(hint),
	}
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
type ToolErrorClassification struct {
	Code      string
	Retryable bool
	Hint      string
	Meta      map[string]any
}

// ToolErrorClassifier 在调用方能识别工具错误时返回分类。
type ToolErrorClassifier func(toolName string, err error) (ToolErrorClassification, bool)

// ClassifyToolError 使用单一中立规则表分类工具错误。
func ClassifyToolError(toolName string, err error) (code string, retryable bool, hint string, meta map[string]any) {
	return ClassifyToolErrorWithClassifier(toolName, err, nil)
}

// ClassifyToolErrorWithClassifier 先识别显式 coded error，再应用调用方和默认规则。
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
	for _, classifier := range defaultToolErrorClassifiers() {
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

// defaultToolErrorClassifiers 返回默认分类表的新切片，避免暴露可变包级规则状态。
func defaultToolErrorClassifiers() []toolErrorClassifier {
	classifiers := defaultToolErrorClassifiersPatch()
	classifiers = append(classifiers, defaultToolErrorClassifiersCore()...)
	classifiers = append(classifiers, defaultToolErrorClassifiersSchemaValidation()...)
	classifiers = append(classifiers, defaultToolErrorClassifiersLaunchCWD()...)
	classifiers = append(classifiers, defaultToolErrorClassifiersLaunchRequest()...)
	classifiers = append(classifiers, defaultToolErrorClassifiersTask()...)
	classifiers = append(classifiers, defaultToolErrorClassifiersLSPService()...)
	classifiers = append(classifiers, defaultToolErrorClassifiersLSPPathMissing()...)
	classifiers = append(classifiers, defaultToolErrorClassifiersLSPPathPolicy()...)
	classifiers = append(classifiers, defaultToolErrorClassifiersLSPTimeout()...)
	classifiers = append(classifiers, defaultToolErrorClassifiersLSPPosition()...)
	classifiers = append(classifiers, defaultToolErrorClassifiersLSPClientAndScope()...)
	return classifiers
}

// defaultToolErrorClassifiersPatch 返回补丁错误分类器。
func defaultToolErrorClassifiersPatch() []toolErrorClassifier {
	return []toolErrorClassifier{
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
				return isEditTool(toolName) && (strings.Contains(message, "ambiguous match") ||
					strings.Contains(message, "multiple candidates matched the patch context"))
			},
		},
	}
}

// defaultToolErrorClassifiersCore 返回核心运行时错误分类器。
func defaultToolErrorClassifiersCore() []toolErrorClassifier {
	return []toolErrorClassifier{
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
	}
}

// defaultToolErrorClassifiersSchemaValidation 返回语言和 schema 参数分类器。
func defaultToolErrorClassifiersSchemaValidation() []toolErrorClassifier {
	return []toolErrorClassifier{
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
	}
}

// defaultToolErrorClassifiersLaunchCWD 返回 agent 启动目录分类器。
func defaultToolErrorClassifiersLaunchCWD() []toolErrorClassifier {
	return []toolErrorClassifier{
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
	}
}

// defaultToolErrorClassifiersLaunchRequest 返回 agent 启动请求分类器。
func defaultToolErrorClassifiersLaunchRequest() []toolErrorClassifier {
	return []toolErrorClassifier{
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
	}
}

// defaultToolErrorClassifiersTask 返回任务请求分类器。
func defaultToolErrorClassifiersTask() []toolErrorClassifier {
	return []toolErrorClassifier{
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
	}
}

// defaultToolErrorClassifiersLSPService 返回 LSP 服务可用性分类器。
func defaultToolErrorClassifiersLSPService() []toolErrorClassifier {
	return []toolErrorClassifier{
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
	}
}

// defaultToolErrorClassifiersLSPPathMissing 返回缺失文件分类器。
func defaultToolErrorClassifiersLSPPathMissing() []toolErrorClassifier {
	return []toolErrorClassifier{
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
	}
}

// defaultToolErrorClassifiersLSPPathPolicy 返回非法路径分类器。
func defaultToolErrorClassifiersLSPPathPolicy() []toolErrorClassifier {
	return []toolErrorClassifier{
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
	}
}

// defaultToolErrorClassifiersLSPTimeout 返回 LSP 超时分类器。
func defaultToolErrorClassifiersLSPTimeout() []toolErrorClassifier {
	return []toolErrorClassifier{
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
	}
}

// defaultToolErrorClassifiersLSPPosition 返回 LSP 位置分类器。
func defaultToolErrorClassifiersLSPPosition() []toolErrorClassifier {
	return []toolErrorClassifier{
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
	}
}

// defaultToolErrorClassifiersLSPClientAndScope 返回 LSP 客户端和范围分类器。
func defaultToolErrorClassifiersLSPClientAndScope() []toolErrorClassifier {
	return []toolErrorClassifier{
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
}

// isDatabaseSchemaMissingMessage 识别常见数据库缺表、缺列及对应 SQLSTATE。
func isDatabaseSchemaMissingMessage(message string) bool {
	return (strings.Contains(message, "relation ") && strings.Contains(message, "does not exist")) ||
		(strings.Contains(message, "column ") && strings.Contains(message, "does not exist")) ||
		strings.Contains(message, "no such table") ||
		strings.Contains(message, "missing database schema") ||
		strings.Contains(message, "sqlstate 42p01") ||
		strings.Contains(message, "sqlstate 42703")
}

// isLaunchRequestInvalidMessage 识别 launch_agent 必填字段和枚举校验错误。
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
	return contract.IsOrchestrationLaunchTool(toolName)
}

func isEditTool(toolName string) bool {
	return strings.EqualFold(strings.TrimSpace(toolName), "patch_edit")
}

func isTaskUpdateNodeTool(toolName string) bool {
	return strings.EqualFold(strings.TrimSpace(toolName), "task_update_node")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
