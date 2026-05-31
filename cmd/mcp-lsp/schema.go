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
	"action":          enumProp("Action", "open_file", "read_file", "diagnostics"),
	"pos":             stringProp("Position as 'file_path:line' (line maps to offset; example internal/foo.go:42). Use this OR file_path+offset, not both."),
	"file_path":       stringProp("File path (absolute or relative, auto-resolved). Alternative to pos for actions that do not need a line."),
	"file_paths":      arrayOfStringsProp("Multiple file paths for batch read or LSP diagnostics filtering"),
	"offset":          integerProp("Start line (1-based, default 1)"),
	"limit":           integerProp("Max lines (default 250, cap 2000)"),
	"expand_comments": booleanProp("Auto-expand starting line upward to include adjacent doc comments (read_file only, default true)"),
}, "action")

var lspInspectSchema = objectSchema(map[string]schema{
	"action": enumProp("Action", "hover", "definition", "implementation", "type_definition", "signature_help"),
	"pos":    stringProp("Position as 'file_path:line:column' (example internal/foo.go:42:9)"),
}, "action", "pos")

var lspXrefSchema = objectSchema(map[string]schema{
	"action":              enumProp("Action", "references", "call_hierarchy", "type_hierarchy"),
	"pos":                 stringProp("Position as 'file_path:line:column' (example internal/foo.go:42:9)"),
	"direction":           enumProp("call_hierarchy: incoming/outgoing/both; type_hierarchy: supertypes/subtypes", "incoming", "outgoing", "both", "supertypes", "subtypes"),
	"include_declaration": booleanProp("Include declaration (references only, default true)"),
	"max_results":         integerProp("Max results (default 30, cap 50)"),
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
}, "action")

var lspEditSchema = objectSchema(map[string]schema{
	"file_path": stringProp("File path (absolute or relative, auto-resolved). Path-only; no :line:column suffix."),
	"patch":     stringProp("Patch body. Each non-header line starts with one prefix: ' '=context (use ' ' for blank context lines, never empty), '-'=remove, '+'=add. Pure-insertion hunks (no '-' line) are rejected; anchor inserts with ' ' context plus '+'. Three accepted forms: (a) implicit single hunk = body only; (b) one explicit '@@ ...' header + body; (c) multiple '@@ ...' hunks. Add 1-2 ' ' context lines around each change to disambiguate when the OldText repeats. Example: ' import \"fmt\"\\n-x := 1\\n+x := 2\\n y := 3'."),
	"version":   integerProp("LSP didChange version counter; let the server default (2) unless you are stitching a specific edit chain."),
}, "file_path", "patch")

var lspCompletionSchema = objectSchema(map[string]schema{
	"pos":         stringProp("Position as 'file_path:line:column' (example internal/foo.go:42:9)"),
	"max_results": integerProp("Max candidates (default 20, cap 50)"),
}, "pos")

var codeRunSchema = objectSchema(map[string]schema{
	"mode":      enumProp("Execution mode", "run", "project_cmd"),
	"language":  stringProp("Language: go, javascript, typescript (required for run mode)"),
	"code":      stringProp("Code snippet (run mode)"),
	"command":   stringProp("Project command (project_cmd mode). Prefer host exec_command for shell/git/package scripts such as npm run lint when available."),
	"auto_wrap": booleanProp("Auto-wrap Go code with package main and imports (default true for Go)"),
	"work_dir":  stringProp("Working directory (must be within workspace root)"),
	"timeout":   integerProp("Timeout in seconds (default 30)"),
}, "mode")

var codeRunTestSchema = objectSchema(map[string]schema{
	"test_func": stringProp("Test function name (e.g. TestMyFunction)"),
	"test_pkg":  stringProp("Package path (e.g. ./internal/engine/executor/, default ./...)"),
	"timeout":   integerProp("Timeout in seconds (default 30)"),
}, "test_func")
