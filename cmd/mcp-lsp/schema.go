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

func arrayOfObjectsProp(desc string) schema {
	return schema{
		"type": "array", "description": desc,
		"items": map[string]any{"type": "object", "additionalProperties": true},
	}
}

func objectSchema(props map[string]schema, required ...string) schema {
	s := schema{
		"type":                 "object",
		"additionalProperties": false,
	}
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
	"action":     enumProp("Operation", "open_file", "read_file", "diagnostics"),
	"file_path":  stringProp("File path (absolute or relative, auto-resolved)"),
	"file_paths": arrayOfStringsProp("Multiple file paths for batch read or diagnostics filtering"),
	"offset":     integerProp("Start line (1-based, default 1)"),
	"limit":      integerProp("Max lines, default 300"),
}, "action")

var lspInspectSchema = objectSchema(map[string]schema{
	"action":    enumProp("Operation", "hover", "definition", "implementation", "type_definition", "signature_help"),
	"file_path": stringProp("File path (absolute or relative, auto-resolved)"),
	"line":      integerProp("Line (1-based)"),
	"column":    integerProp("Column (1-based)"),
}, "action", "file_path", "line", "column")

var lspXrefSchema = objectSchema(map[string]schema{
	"action":              enumProp("Operation", "references", "call_hierarchy", "type_hierarchy"),
	"file_path":           stringProp("File path (absolute or relative, auto-resolved)"),
	"line":                integerProp("Line (1-based)"),
	"column":              integerProp("Column (1-based)"),
	"direction":           enumProp("call_hierarchy: incoming/outgoing/both; type_hierarchy: supertypes/subtypes", "incoming", "outgoing", "both", "supertypes", "subtypes"),
	"include_declaration": booleanProp("Include declaration (references only)"),
	"verbosity":           enumProp("Output verbosity", "compact", "full"),
	"max_results":         integerProp("Max results (default compact=30, full=50, cap 50)"),
}, "action", "file_path", "line", "column")

var lspGrepSchema = objectSchema(map[string]schema{
	"action":         enumProp("Operation", "text_search", "ast_search"),
	"query":          stringProp("Search query"),
	"path":           stringProp("Search root"),
	"glob":           stringProp("Glob filter (text_search only)"),
	"language":       stringProp("Language for AST"),
	"regex":          booleanProp("Regex mode (default: literal)"),
	"case_sensitive": booleanProp("Case sensitive (text_search only)"),
	"max_results":    integerProp("Max matches"),
}, "action")

var lspStructureSchema = objectSchema(map[string]schema{
	"action":      enumProp("Operation", "document_symbol", "workspace_symbol", "folding_range", "semantic_tokens"),
	"file_path":   stringProp("File path (absolute or relative, auto-resolved)"),
	"query":       stringProp("Symbol query"),
	"language":    stringProp("Language filter"),
	"verbosity":   enumProp("Output verbosity", "compact", "full"),
	"max_results": integerProp("Max results (default compact=20, full=50, cap 50)"),
}, "action")

var lspEditSchema = objectSchema(map[string]schema{
	"action":    enumProp("Operation", "rename", "code_action", "format", "replace_range"),
	"file_path": stringProp("File path (absolute or relative, auto-resolved)"),
	"line":      integerProp("1-based line (for rename/code_action)"),
	"column":    integerProp("1-based column (for rename/code_action)"),
	"end_line":  integerProp("End line for code_action range"),
	"end_column": integerProp("End column for code_action range"),
	"patch":     stringProp("Single-hunk patch text for replace_range"),
	"edits":     arrayOfObjectsProp("Array of edits for replace_range: [{old_string, new_string}]"),
	"new_name":  stringProp("New name for rename"),
	"new_text":  stringProp("Legacy rename alias; replacement text for replace_range"),
	"only":      arrayOfStringsProp("Code action kinds filter"),
}, "action", "file_path")

var lspCompletionSchema = objectSchema(map[string]schema{
	"file_path":   stringProp("File path (absolute or relative, auto-resolved)"),
	"line":        integerProp("Line (1-based)"),
	"column":      integerProp("Column (1-based)"),
	"verbosity":   enumProp("Output verbosity", "compact", "full"),
	"max_results": integerProp("Max candidates (default compact=20, full=50, cap 50)"),
}, "file_path", "line", "column")

var codeRunSchema = objectSchema(map[string]schema{
	"mode":      enumProp("Execution mode", "run", "project_cmd"),
	"language":  stringProp("Language: go, javascript, typescript. Required for run mode."),
	"code":      stringProp("Code snippet to execute (for run mode)"),
	"command":   stringProp("Shell command (for project_cmd mode)"),
	"auto_wrap": booleanProp("Auto-wrap Go code with package main and imports. Default: true for Go"),
	"work_dir":  stringProp("Custom working directory (must be within project root)"),
	"timeout":   integerProp("Timeout in seconds. Default: 30"),
}, "mode")

var codeRunTestSchema = objectSchema(map[string]schema{
	"test_func": stringProp("Test function name (e.g. TestMyFunction)"),
	"test_pkg":  stringProp("Package path (e.g. ./internal/engine/executor/). Default: ./..."),
	"timeout":   integerProp("Timeout in seconds. Default: 30"),
}, "test_func")
