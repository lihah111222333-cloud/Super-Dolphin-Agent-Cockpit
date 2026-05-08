package middleware

type toolOverflowHint struct {
	Hint       string
	NextAction map[string]any
}

var toolOverflowHints = map[string]toolOverflowHint{
	"lsp_grep": {
		Hint: "Narrow search: add path/glob filter, or reduce max_results",
		NextAction: map[string]any{
			"tool":         "lsp_grep",
			"suggest_args": map[string]any{"max_results": 10},
			"tip":          "Scope search to a subdirectory or single file",
		},
	},
	"lsp_file": {
		Hint: "Use offset/limit pagination to read file in chunks",
		NextAction: map[string]any{
			"tool":         "lsp_file",
			"suggest_args": map[string]any{"limit": 100},
			"tip":          "Read a specific range with offset and limit",
		},
	},
	"lsp_inspect": {
		Hint: "Hover result is large; try a more specific location",
	},
	"lsp_xref": {
		Hint: "Use compact verbosity or reduce max_results",
		NextAction: map[string]any{
			"tool":         "lsp_xref",
			"suggest_args": map[string]any{"verbosity": "compact", "max_results": 10},
		},
	},
	"lsp_structure": {
		Hint: "Use compact verbosity or limit to document_symbol action",
	},
	"lsp_edit": {
		Hint: "Edit result truncated; check success/applied fields for status",
	},
	"lsp_completion": {
		Hint: "Too many completions; use a more specific prefix",
		NextAction: map[string]any{
			"tool":         "lsp_completion",
			"suggest_args": map[string]any{"max_results": 10},
		},
	},
	"code_run": {
		Hint: "Command output too large; pipe through head/tail or redirect to file",
	},
	"code_run_test": {
		Hint: "Test output too large; run a single test function or check -v flag",
	},
}

func lookupHint(toolName string) toolOverflowHint {
	if h, ok := toolOverflowHints[toolName]; ok {
		return h
	}
	return toolOverflowHint{Hint: "Result too large; try narrowing the query"}
}

func extractSummary(toolName string, payload map[string]any) map[string]any {
	if payload == nil {
		return map[string]any{}
	}
	switch toolName {
	case "lsp_grep":
		s := map[string]any{
			"total":   numericField(payload, "total"),
			"showing": numericField(payload, "showing"),
		}
		if files, ok := payload["files"].(map[string]any); ok {
			names := make([]string, 0, 5)
			for k := range files {
				names = append(names, k)
				if len(names) >= 5 {
					break
				}
			}
			s["top_files"] = names
		}
		return s
	case "lsp_xref":
		return map[string]any{
			"total":   numericField(payload, "total"),
			"showing": numericField(payload, "showing"),
		}
	case "lsp_edit":
		return map[string]any{
			"success": payload["success"],
			"applied": payload["applied"],
			"action":  payload["action"],
		}
	default:
		return map[string]any{}
	}
}
