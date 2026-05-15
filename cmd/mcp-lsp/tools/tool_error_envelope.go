package tools

import (
	"context"
	"errors"
	"os"
	"strings"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
)

// ToolErrorEnvelope is the language-neutral error payload shape shared by LSP
// tools. P2-01 keeps this contract independent from Go/gopls so later P2-02
// transport wiring can render the same envelope for every language adapter.
type ToolErrorEnvelope struct {
	Success   bool           `json:"success"`
	Error     string         `json:"error"`
	Code      string         `json:"code,omitempty"`
	Retryable bool           `json:"retryable,omitempty"`
	Hint      string         `json:"hint,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}

func newToolErrorEnvelope(toolName, languageID string, err error) ToolErrorEnvelope {
	code, retryable, hint := classifyToolError(err)
	meta := map[string]any{"tool": strings.TrimSpace(toolName)}
	if languageID = normalizeToolLanguageID(languageID); languageID != "" {
		meta["language_id"] = languageID
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

func classifyToolError(err error) (code string, retryable bool, hint string) {
	if err == nil {
		return "unknown", false, "No tool error was provided."
	}
	message := strings.ToLower(err.Error())
	for _, classifier := range toolErrorClassifiers {
		if classifier.match(err, message) {
			return classifier.code, classifier.retryable, classifier.hint
		}
	}
	return "lsp_unavailable", true, "Retry if the language server is starting; otherwise inspect manager diagnostics."
}

type toolErrorClassifier struct {
	code      string
	retryable bool
	hint      string
	match     func(error, string) bool
}

var toolErrorClassifiers = []toolErrorClassifier{
	{
		code: "language_unsupported",
		hint: "Choose a file or language_id with a registered language adapter.",
		match: func(err error, message string) bool {
			return errors.Is(err, lspmanager.ErrUnsupportedLanguage) || strings.Contains(message, "unsupported language")
		},
	},
	{
		code: "capability_unsupported",
		hint: "Use a tool/action supported by this language adapter.",
		match: func(_ error, message string) bool {
			return strings.Contains(message, "unsupported") && strings.Contains(message, "capability")
		},
	},
	{
		code: "schema_invalid",
		hint: "Check the tool schema: use the documented field names and JSON value types.",
		match: func(_ error, message string) bool {
			return strings.Contains(message, "decode params") || strings.Contains(message, "unknown field") || strings.Contains(message, "json:")
		},
	},
	{
		code: "file_not_found",
		hint: "Verify file_path is under the trusted workspace and exists on disk.",
		match: func(err error, message string) bool {
			return errors.Is(err, os.ErrNotExist) || strings.Contains(message, "not found") || strings.Contains(message, "no such file")
		},
	},
	{
		code:      "lsp_timeout",
		retryable: true,
		hint:      "Retry with a narrower query, smaller max_results, or after the language server finishes indexing.",
		match: func(err error, message string) bool {
			return errors.Is(err, context.DeadlineExceeded) || strings.Contains(message, "timeout") || strings.Contains(message, "deadline")
		},
	},
	{
		code: "position_invalid",
		hint: "Line and column inputs are 1-based; move the cursor onto an identifier.",
		match: func(_ error, message string) bool {
			return strings.Contains(message, "line") && strings.Contains(message, "column")
		},
	},
	{
		code:      "lsp_client_closed",
		retryable: true,
		hint:      "Retry once so the manager can recreate the language server client.",
		match: func(_ error, message string) bool {
			return strings.Contains(message, "client") && strings.Contains(message, "closed")
		},
	},
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func normalizeToolLanguageID(languageID string) string {
	return strings.ToLower(strings.TrimSpace(languageID))
}
