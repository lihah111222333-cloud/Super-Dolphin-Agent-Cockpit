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
	"file_path":       stringProp("File path"),
	"file_paths":      arrayOfStringsProp("Multiple file paths"),
	"language_id":     stringProp("Language override (optional)"),
	"offset":          integerProp("Start line (1-based, default 1)"),
	"limit":           integerProp("Max lines (default 250)"),
	"expand_comments": booleanProp("Automatically expand starting line upwards to include adjacent comments (default true)"),
}, "action")

var lspInspectSchema = objectSchema(map[string]schema{
	"action":      enumProp("Action", "hover", "definition", "implementation", "type_definition", "signature_help"),
	"pos":         stringProp("Position in format 'file_path:line:column'"),
	"language_id": stringProp("Language override (optional)"),
}, "action", "pos")

var lspXrefSchema = objectSchema(map[string]schema{
	"action":              enumProp("Action", "references", "call_hierarchy", "type_hierarchy"),
	"pos":                 stringProp("Position in format 'file_path:line:column'"),
	"language_id":         stringProp("Language override (optional)"),
	"direction":           enumProp("Direction (hierarchy)", "incoming", "outgoing", "both", "supertypes", "subtypes"),
	"include_declaration": booleanProp("Include declaration"),
	"verbosity":           enumProp("Verbosity", "compact", "full"),
	"max_results":         integerProp("Max results"),
}, "action", "pos")

var lspGrepSchema = objectSchema(map[string]schema{
	"action":         enumProp("Action", "text_search", "ast_search"),
	"query":          stringProp("Search query"),
	"path":           stringProp("Search root"),
	"glob":           stringProp("Glob filter"),
	"language":       stringProp("Language"),
	"regex":          booleanProp("Regex mode"),
	"case_sensitive": booleanProp("Case sensitive"),
	"max_results":    integerProp("Max matches"),
}, "action")

var lspGrepOutputSchema = schema{
	"type": "object",
	"properties": map[string]any{
		"files":               map[string]any{"type": "object", "description": "matched files"},
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
	"action":      enumProp("Action", "document_symbol", "workspace_symbol", "folding_range", "semantic_tokens"),
	"file_path":   stringProp("File path"),
	"language_id": stringProp("Language override (optional)"),
	"query":       stringProp("Symbol query"),
	"language":    stringProp("Language filter"),
	"verbosity":   enumProp("Verbosity", "compact", "full"),
	"max_results": integerProp("Max results"),
}, "action")

var lspEditSchema = objectSchema(map[string]schema{
	"file_path":   stringProp("File path"),
	"language_id": stringProp("Language override (optional)"),
	"patch":       stringProp("Patch to apply"),
	"version":     integerProp("Document version"),
}, "file_path", "patch")

var lspCompletionSchema = objectSchema(map[string]schema{
	"pos":         stringProp("Position in format 'file_path:line:column'"),
	"language_id": stringProp("Language override (optional)"),
	"verbosity":   enumProp("Verbosity", "compact", "full"),
	"max_results": integerProp("Max candidates"),
}, "pos")

var codeRunSchema = objectSchema(map[string]schema{
	"mode":      enumProp("Mode", "run", "project_cmd"),
	"language":  stringProp("Language (go, javascript, typescript)"),
	"code":      stringProp("Code snippet"),
	"command":   stringProp("Shell command"),
	"auto_wrap": booleanProp("Auto-wrap Go code"),
	"work_dir":  stringProp("Working directory"),
	"timeout":   integerProp("Timeout in seconds"),
}, "mode")

var codeRunTestSchema = objectSchema(map[string]schema{
	"test_func": stringProp("Test function name"),
	"test_pkg":  stringProp("Package path"),
	"timeout":   integerProp("Timeout in seconds"),
}, "test_func")
