package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/jackc/pgx/v5/pgconn"
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

func (e *CodedToolError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *CodedToolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewCodedToolError(code string, err error, retryable bool, hint string) error {
	if err == nil {
		err = errors.New(strings.TrimSpace(code))
	}
	return &CodedToolError{Err: err, Code: strings.TrimSpace(code), Retryable: retryable, Hint: strings.TrimSpace(hint)}
}

func NewPanicToolError(recovered any) error {
	return &CodedToolError{
		Err:       fmt.Errorf("panic recovered: %v", recovered),
		Code:      "internal_panic",
		Retryable: false,
		Hint:      "The tool handler panicked; inspect logs and retry only after the bug is fixed.",
	}
}

func NewToolErrorEnvelope(toolName string, err error) ToolErrorEnvelope {
	return NewToolErrorEnvelopeWithMeta(toolName, "", err, nil)
}

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

func ClassifyToolError(toolName string, err error) (code string, retryable bool, hint string, meta map[string]any) {
	if err == nil {
		return "unknown", false, "No tool error was provided.", nil
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
	return "tool_error", false, "Inspect the tool error message and logs; retry only after fixing the reported issue.", nil
}

type toolErrorClassifier struct {
	code      string
	retryable bool
	hint      func(toolName, message string) string
	match     func(error, string, string) bool
}

var toolErrorClassifiers = []toolErrorClassifier{
	{
		code: "database_schema_missing",
		hint: staticToolHint("The database schema is missing or behind; start the service with migration lifecycle enabled or apply migrations before retrying."),
		match: func(err error, message string, _ string) bool {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) {
				return pgErr.Code == "42P01" || pgErr.Code == "42703"
			}
			return strings.Contains(message, "relation ") && strings.Contains(message, "does not exist") ||
				strings.Contains(message, "column ") && strings.Contains(message, "does not exist")
		},
	},
	{
		code: "internal_panic",
		hint: staticToolHint("The tool handler panicked; inspect logs and retry only after the bug is fixed."),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "panic recovered")
		},
	},
	{
		code: "capability_unsupported",
		hint: staticToolHint("Use a tool/action/language supported by this helper or language adapter."),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "unsupported run language") ||
				strings.Contains(message, "unsupported helper language") ||
				(strings.Contains(message, "unsupported") && strings.Contains(message, "capability"))
		},
	},
	{
		code: "language_unsupported",
		hint: staticToolHint("Choose a file or language_id with a registered language adapter."),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "unsupported language") ||
				strings.Contains(message, "unsupported language adapter") ||
				strings.Contains(message, "unsupported language for lsp toolchain")
		},
	},
	{
		code: "schema_invalid",
		hint: staticToolHint("Check the tool schema: use documented field names and JSON value types."),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "decode params") ||
				strings.Contains(message, "decode ") ||
				strings.Contains(message, "unknown field") ||
				strings.Contains(message, "json:")
		},
	},
	{
		code: "cwd_required",
		hint: staticToolHint("Pass a non-empty cwd, or pass parent_id for an existing parent agent with cwd."),
		match: func(err error, message string, toolName string) bool {
			return isLaunchAgentTool(toolName) && (errors.Is(err, contract.ErrLaunchCWDRequired) ||
				strings.Contains(message, "launch_agent cwd is required") ||
				strings.Contains(message, "thread start cwd is required"))
		},
	},
	{
		code: "cwd_invalid",
		hint: staticToolHint("Pass an explicit absolute cwd path; dot and relative cwd are not accepted."),
		match: func(err error, message string, toolName string) bool {
			return isLaunchAgentTool(toolName) && (errors.Is(err, contract.ErrLaunchCWDInvalid) ||
				strings.Contains(message, "cwd must be explicit") ||
				strings.Contains(message, "cwd must be an absolute path"))
		},
	},
	{
		code: "provider_required",
		hint: staticToolHint("Pass provider as codex or claude, or use launch_agent without provider so the tool can default it to codex."),
		match: func(_ error, message string, toolName string) bool {
			return isLaunchAgentTool(toolName) && strings.Contains(message, "provider is required")
		},
	},
	{
		code: "provider_invalid",
		hint: staticToolHint("Pass provider as codex or claude."),
		match: func(_ error, message string, toolName string) bool {
			return isLaunchAgentTool(toolName) && strings.Contains(message, "invalid provider")
		},
	},
	{
		code: "launch_request_invalid",
		hint: staticToolHint("Fix the launch_agent arguments and retry; required fields must be non-empty and enum values must be supported."),
		match: func(_ error, message string, toolName string) bool {
			return isLaunchAgentTool(toolName) && isLaunchRequestInvalidMessage(message)
		},
	},
	{
		code: "database_schema_missing",
		hint: staticToolHint("Run database migrations or verify the embedded database schema before retrying."),
		match: func(_ error, message string, _ string) bool {
			return (strings.Contains(message, "relation ") && strings.Contains(message, " does not exist")) ||
				strings.Contains(message, "no such table") ||
				strings.Contains(message, "missing database schema")
		},
	},
	{
		code: "invalid_input",
		hint: staticToolHint("Fix the task DAG request, node status, or transition inputs before retrying."),
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
		hint:      staticToolHint("Retry if the language server is starting; otherwise inspect manager diagnostics."),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "language server is starting") ||
				(strings.Contains(message, "language server") && (strings.Contains(message, "not ready") || strings.Contains(message, "unavailable"))) ||
				(strings.Contains(message, "lsp") && (strings.Contains(message, "not ready") || strings.Contains(message, "unavailable")))
		},
	},
	{
		code: "dependency_missing",
		hint: staticToolHint("Install ast-grep or ensure sg is available in PATH."),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "sg not found in path")
		},
	},
	{
		code: "file_not_found",
		hint: staticToolHint("Verify file_path is under the trusted workspace and exists on disk."),
		match: func(err error, message string, _ string) bool {
			return errors.Is(err, os.ErrNotExist) ||
				strings.Contains(message, "not found") ||
				strings.Contains(message, "no such file") ||
				strings.Contains(message, "no rows in result set")
		},
	},
	{
		code: "path_invalid",
		hint: staticToolHint("Pass a regular file path for file actions that require one; directories are not valid file_path values."),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "must reference a regular file") ||
				strings.Contains(message, "is not a regular file") ||
				strings.Contains(message, "must be a regular file")
		},
	},
	{
		code: "path_outside_workspace",
		hint: staticToolHint("Use a path under the trusted workspace roots, or add the directory to the workspace root set."),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "outside workspace roots") ||
				strings.Contains(message, "outside allowed workspace roots")
		},
	},
	{
		code:      "lsp_timeout",
		retryable: true,
		hint:      staticToolHint("Retry with a narrower query, smaller max_results, or after the language server finishes indexing."),
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
				return "Line/column inputs are 1-based; for replace_range coordinate errors, prefer patch or edits when possible."
			}
			return "Line and column inputs are 1-based; move the cursor onto an identifier."
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
		hint:      staticToolHint("Retry once so the manager can recreate the language server client."),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "client") && strings.Contains(message, "closed")
		},
	},
	{
		code: "scope_ambiguous",
		hint: staticToolHint("Provide exactly one unambiguous scope selector such as file_path or language."),
		match: func(_ error, message string, _ string) bool {
			return strings.Contains(message, "ambiguous") ||
				strings.Contains(message, "exactly one of") ||
				strings.Contains(message, "could not resolve scope")
		},
	},
}

func isLaunchRequestInvalidMessage(message string) bool {
	return strings.Contains(message, " is required") ||
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

func isTaskUpdateNodeTool(toolName string) bool {
	return strings.ToLower(strings.TrimSpace(toolName)) == "task_update_node"
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
