package main

// schema helpers — mirrors cmd/mcp-orch/tools/types.go pattern.

type schema = map[string]any

func stringProp(desc string) schema {
	return schema{"type": "string", "description": desc}
}

func integerProp(desc string) schema {
	return schema{"type": "integer", "description": desc}
}

func booleanProp(desc string) schema {
	return schema{"type": "boolean", "description": desc}
}

func enumProp(desc string, values ...string) schema {
	return schema{"type": "string", "description": desc, "enum": values}
}

func arrayOfStringsProp(desc string) schema {
	return schema{"type": "array", "description": desc, "items": map[string]any{"type": "string"}}
}

func objectSchema(props map[string]schema, required ...string) schema {
	s := schema{"type": "object", "additionalProperties": false}
	if len(props) > 0 {
		mapped := make(map[string]any, len(props))
		for k, v := range props {
			mapped[k] = map[string]any(v)
		}
		s["properties"] = mapped
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// ---------------------------------------------------------------------------
// Per-tool schemas
// ---------------------------------------------------------------------------

var lspFileSchema = objectSchema(map[string]schema{
	"action":     enumProp("Action", "open_file", "read_file", "diagnostics"),
	"pos":        stringProp("Position: 'file_path' (full file) or 'file_path:line' (function at line). Example: internal/foo.go:42 reads the function containing line 42 with its doc comments."),
	"scope":      enumProp("Read mode override (default: function at line). Pass scope=lines to force a line-window read instead of function extraction.", "lines"),
	"file_path":  stringProp("File path for open_file/diagnostics when pos is not used."),
	"file_paths": arrayOfStringsProp("Multiple file paths for batch read or diagnostics"),
	"limit":      integerProp("Max lines to return (default 300 for function mode, 250 for line-window; cap 2000). In function mode this caps the output without switching to line-window."),
	"work_dir":   lspWorkDirProp(),
}, "action")

var lspInspectSchema = objectSchema(map[string]schema{
	"action":   enumProp("Action", "hover", "definition", "implementation", "type_definition", "signature_help"),
	"pos":      stringProp("Position as 'file_path:line:column' (example internal/foo.go:42:9)"),
	"work_dir": lspWorkDirProp(),
}, "action", "pos")

var lspXrefSchema = objectSchema(map[string]schema{
	"action":              enumProp("Action", "references", "call_hierarchy", "type_hierarchy"),
	"pos":                 stringProp("Position as 'file_path:line:column' (example internal/foo.go:42:9)"),
	"direction":           enumProp("call_hierarchy: incoming/outgoing/both; type_hierarchy: supertypes/subtypes", "incoming", "outgoing", "both", "supertypes", "subtypes"),
	"include_declaration": booleanProp("Include declaration (references only, default true)"),
	"max_results":         integerProp("Max results (default 30, cap 50)"),
	"work_dir":            lspWorkDirProp(),
}, "action", "pos")

var lspGrepSchema = objectSchema(map[string]schema{
	"action":         enumProp("Action", "text_search", "ast_search"),
	"query":          stringProp("Search query"),
	"path":           stringProp("Search root"),
	"glob":           stringProp("Glob filter (text_search only)"),
	"language":       stringProp("Language for AST"),
	"regex":          booleanProp("Regex mode (default literal)"),
	"case_sensitive": booleanProp("Case sensitive (text_search only)"),
	"max_results":    integerProp("Max matches (default 50, cap 50)"),
	"work_dir":       lspWorkDirProp(),
}, "action")

var lspGrepOutputSchema = schema{
	"type": "object",
	"properties": map[string]any{
		"files":               map[string]any{"type": "object", "description": "matched files keyed by path; each value has cols and rows"},
		"total":               map[string]any{"type": "integer"},
		"showing":             map[string]any{"type": "integer"},
		"truncated":           map[string]any{"type": "boolean"},
		"dropped_for_payload": map[string]any{"type": "integer"},
		"regex_fallback":      map[string]any{"type": "boolean"},
		"message":             map[string]any{"type": "string"},
		"hint":                map[string]any{"type": "string"},
	},
	"required": []string{"total"},
}

var lspStructureSchema = objectSchema(map[string]schema{
	"action":      enumProp("Action", "document_symbol", "workspace_symbol"),
	"file_path":   stringProp("File path (absolute or relative, auto-resolved). Path-only; no :line:column suffix."),
	"path":        stringProp("Legacy alias for file_path"),
	"query":       stringProp("Symbol query (workspace_symbol only)"),
	"language":    stringProp("Language filter (workspace_symbol only)"),
	"max_results": integerProp("Max results (default 20, cap 50)"),
	"work_dir":    lspWorkDirProp(),
}, "action")

var lspEditSchema = objectSchema(map[string]schema{
	"action":    enumProp("Action (default: replace_range)", "replace_range", "rename", "code_action", "format"),
	"file_path": stringProp("File path (absolute or relative, auto-resolved). Required for replace_range and format."),
	"patch":     stringProp("Patch body for replace_range. Each non-header line starts with one prefix: ' '=context (use ' ' for blank context lines, never empty), '-'=remove, '+'=add. Pure-insertion hunks (no '-' line) are rejected; anchor inserts with ' ' context plus '+'. Three accepted forms: (a) implicit single hunk = body only; (b) one explicit '@@ ...' header + body; (c) multiple '@@ ...' hunks."),
	"version":   integerProp("LSP didChange version counter; let the server default (2) unless you are stitching a specific edit chain."),
	"pos":       stringProp("Position as 'file_path:line:column' for rename/code_action (example internal/foo.go:42:9)."),
	"new_name":  stringProp("New symbol name (rename only)."),
	"only":      arrayOfStringsProp("Code action kinds filter (code_action only, e.g. [\"quickfix\", \"refactor\"])."),
	"work_dir":  lspWorkDirProp(),
}, "action")

var lspCompletionSchema = objectSchema(map[string]schema{
	"pos":         stringProp("Position as 'file_path:line:column' (example internal/foo.go:42:9)"),
	"max_results": integerProp("Max candidates (default 20, cap 50)"),
	"work_dir":    lspWorkDirProp(),
}, "pos")

var codeRunSchema = objectSchema(map[string]schema{
	"mode":      enumProp("Execution mode", "run", "project_cmd"),
	"language":  stringProp("Language: go, javascript, typescript (required for run mode)"),
	"code":      stringProp("Code snippet (run mode)"),
	"command":   stringProp("Project command (project_cmd mode). Prefer host exec_command for shell/git/package scripts such as npm run lint when available."),
	"auto_wrap": booleanProp("Auto-wrap Go code with package main and imports (default true for Go)"),
	"work_dir":  stringProp("Working directory. Absolute paths are accepted as the explicit execution root; relative paths resolve under the trusted workspace root."),
	"timeout":   integerProp("Timeout in seconds (default 30)"),
}, "mode")

var codeRunTestSchema = objectSchema(map[string]schema{
	"test_func": stringProp("Test function name (e.g. TestMyFunction)"),
	"test_pkg":  stringProp("Package path (e.g. ./internal/engine/executor/, default ./...)"),
	"timeout":   integerProp("Timeout in seconds (default 30)"),
	"work_dir":  lspWorkDirProp(),
}, "test_func")

func lspWorkDirProp() schema {
	return stringProp("Explicit working directory for this tool call. Absolute paths are accepted as the call's trusted workspace root; relative tool paths resolve under it.")
}
