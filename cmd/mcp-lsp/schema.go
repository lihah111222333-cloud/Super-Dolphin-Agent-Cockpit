// Package main 是 mcp-lsp sidecar 进程的入口，通过 MCP stdio 协议暴露 LSP 工具能力。
package main

// schema helpers 统一生成 MCP 工具输入 schema，保持各工具声明的 JSON 形状一致。

type schema = map[string]any

// stringProp 生成字符串属性 schema，并透传面向工具调用方的说明文本。
func stringProp(desc string) schema {
	return schema{"type": "string", "description": desc}
}

// integerProp 生成整数属性 schema，供位置、数量和限制类字段复用。
func integerProp(desc string) schema {
	return schema{"type": "integer", "description": desc}
}

// booleanProp 生成布尔属性 schema，用于开关类工具参数。
func booleanProp(desc string) schema {
	return schema{"type": "boolean", "description": desc}
}

// enumProp 生成带枚举约束的字符串属性 schema，限制 action/direction 等固定取值。
func enumProp(desc string, values ...string) schema {
	return schema{"type": "string", "description": desc, "enum": values}
}

// arrayOfStringsProp 生成字符串数组属性 schema，用于多路径或过滤条件列表。
func arrayOfStringsProp(desc string) schema {
	return schema{"type": "array", "description": desc, "items": map[string]any{"type": "string"}}
}

// NewObjectSchema 生成关闭 additionalProperties 的对象 schema。
// 未声明字段会在工具解码阶段被拒绝，避免调用方拼错参数后静默忽略。
func NewObjectSchema(props map[string]schema, required ...string) schema {
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

// withActionConditions 将每个 action 的条件约束附加到工具对象 schema。
func withActionConditions(s schema, conditions ...schema) schema {
	allOf := make([]any, 0, len(conditions))
	for _, condition := range conditions {
		allOf = append(allOf, map[string]any(condition))
	}
	s["allOf"] = allOf
	return s
}

// actionCondition 只在 action 精确匹配时应用 then schema。
func actionCondition(action string, then schema) schema {
	return schema{
		"if": map[string]any{
			"properties": map[string]any{
				"action": map[string]any{"const": action},
			},
			"required": []string{"action"},
		},
		"then": map[string]any(then),
	}
}

// exactlyOneRequired 要求给定字段中恰好出现一个。
func exactlyOneRequired(fields ...string) []any {
	choices := make([]any, 0, len(fields))
	for _, field := range fields {
		choices = append(choices, map[string]any{"required": []string{field}})
	}
	return choices
}

// forbidFields 拒绝出现任何与当前 action 无关的字段。
func forbidFields(fields ...string) schema {
	forbidden := make([]any, 0, len(fields))
	for _, field := range fields {
		forbidden = append(forbidden, map[string]any{"required": []string{field}})
	}
	return schema{"not": map[string]any{"anyOf": forbidden}}
}

// 各工具 schema 按 MCP 暴露的 action 分组，字段说明就是模型可见的调用约束。

func newLSPFileSchema() schema {
	openFile := forbidFields("pos", "file_paths")
	openFile["required"] = []string{"file_path"}
	return withActionConditions(NewObjectSchema(map[string]schema{
		"action":      enumProp("Action. Use diagnostics on this file tool to fetch LSP diagnostics; there is no separate diagnostics tool.", "open_file", "read_file", "diagnostics"),
		"pos":         stringProp("Position: 'file_path' (full file) or 'file_path:line' (function at line). Example: internal/foo.go:42 reads the function containing line 42 with its doc comments."),
		"scope":       enumProp("Read mode override (default: function at line). Pass scope=lines to force a line-window read instead of function extraction.", "lines"),
		"file_path":   stringProp(`File path for open_file or diagnostics. Example: {"action":"diagnostics","file_path":"internal/foo.go"}.`),
		"file_paths":  arrayOfStringsProp(`Multiple file paths for batch read or diagnostics. Example: {"action":"diagnostics","file_paths":["internal/foo.go"]}.`),
		"language_id": stringProp("Optional language server override for extensionless or ambiguous files."),
		"limit":       integerProp("Max lines to return for read_file (default 300 for function mode, 250 for line-window; cap 2000). Single-file read_file output is budgeted by final text at 50 KiB and may be truncated with a continuation hint."),
		"work_dir":    lspWorkDirProp(),
	}, "action"),
		actionCondition("open_file", openFile),
		actionCondition("read_file", schema{
			"oneOf": exactlyOneRequired("pos", "file_path", "file_paths"),
		}),
		actionCondition("diagnostics", schema{
			"oneOf": exactlyOneRequired("file_path", "file_paths"),
		}),
	)
}

func newLSPInspectSchema() schema {
	return NewObjectSchema(map[string]schema{
		"action":      enumProp("Action", "hover", "definition", "implementation", "type_definition", "signature_help"),
		"pos":         stringProp("Position as 'file_path:line:column' (example internal/foo.go:42:9)"),
		"language_id": stringProp("Optional language server override for extensionless or ambiguous files."),
		"work_dir":    lspWorkDirProp(),
	}, "action", "pos")
}

func newLSPXrefSchema() schema {
	references := forbidFields("direction")
	references["required"] = []string{"pos"}
	callHierarchy := forbidFields("include_declaration")
	callHierarchy["required"] = []string{"pos"}
	callHierarchy["properties"] = map[string]any{
		"direction": map[string]any{"enum": []string{"incoming", "outgoing", "both"}},
	}
	typeHierarchy := forbidFields("include_declaration")
	typeHierarchy["required"] = []string{"pos"}
	typeHierarchy["properties"] = map[string]any{
		"direction": map[string]any{"enum": []string{"supertypes", "subtypes", "both"}},
	}
	return withActionConditions(NewObjectSchema(map[string]schema{
		"action":              enumProp("Action", "references", "call_hierarchy", "type_hierarchy"),
		"pos":                 stringProp("Position as 'file_path:line:column' (example internal/foo.go:42:9)"),
		"direction":           enumProp("call_hierarchy: incoming/outgoing/both; type_hierarchy: supertypes/subtypes/both", "incoming", "outgoing", "both", "supertypes", "subtypes"),
		"include_declaration": booleanProp("Include declaration (references only, default true)"),
		"language_id":         stringProp("Optional language server override for extensionless or ambiguous files."),
		"max_results":         integerProp("Max results (default 30, cap 50)"),
		"work_dir":            lspWorkDirProp(),
	}, "action"),
		actionCondition("references", references),
		actionCondition("call_hierarchy", callHierarchy),
		actionCondition("type_hierarchy", typeHierarchy),
	)
}

func newLSPGrepSchema() schema {
	paths := arrayOfStringsProp("Search roots for text_search or ast_search. A single root is a one-element array.")
	paths["minItems"] = 1
	return withActionConditions(NewObjectSchema(map[string]schema{
		"action":         enumProp("Action", "text_search", "ast_search"),
		"ast_language":   stringProp("Registered AST language selector for ast_search; omit only when a file path or glob identifies it unambiguously."),
		"query":          stringProp("Search query"),
		"paths":          paths,
		"glob":           stringProp("Glob filter for text_search or ast_search; ast_search can also use it to infer the target language."),
		"regex":          booleanProp("Regex mode (default literal)"),
		"case_sensitive": booleanProp("Override smart-case (default: sensitive when query has uppercase, insensitive otherwise)"),
		"max_results":    integerProp("Max matches per file (default 50, cap 50)"),
		"work_dir":       lspWorkDirProp(),
	}, "action", "query"),
		actionCondition("text_search", forbidFields("ast_language")),
		actionCondition("ast_search", forbidFields("regex", "case_sensitive")),
	)
}

func newLSPGrepOutputSchema() schema {
	return schema{
		"type": "object",
		"properties": map[string]any{
			"data":                map[string]any{"type": "object", "description": "matched files keyed by path; each value has cols and rows"},
			"total":               map[string]any{"type": "integer", "description": "total filtered matches before display limits"},
			"showing":             map[string]any{"type": "integer", "description": "rows currently shown across all files"},
			"truncated":           map[string]any{"type": "boolean", "description": "true when per-file or payload limits omitted rows"},
			"dropped_for_payload": map[string]any{"type": "integer"},
			"regex_fallback":      map[string]any{"type": "boolean"},
			"message":             map[string]any{"type": "string"},
			"hint":                map[string]any{"type": "string"},
		},
		"required": []string{"total"},
	}
}

func newLSPStructureSchema() schema {
	workspaceSymbol := schema{
		"required": []string{"query"},
		"oneOf": []any{
			map[string]any{
				"required": []string{"file_path"},
				"not":      map[string]any{"required": []string{"workspace_language"}},
			},
			map[string]any{
				"required": []string{"workspace_language"},
				"not": map[string]any{"anyOf": []any{
					map[string]any{"required": []string{"file_path"}},
					map[string]any{"required": []string{"language_id"}},
				}},
			},
		},
	}
	return withActionConditions(NewObjectSchema(map[string]schema{
		"action":             enumProp("Action", "document_symbol", "workspace_symbol", "folding_range", "semantic_tokens"),
		"file_path":          stringProp("File path (absolute or relative, auto-resolved). Required for document_symbol, folding_range, and semantic_tokens. For workspace_symbol, pass exactly one of file_path or workspace_language. Path-only; no :line:column suffix."),
		"query":              stringProp("Symbol query. Required for workspace_symbol; ignored by document_symbol, folding_range, and semantic_tokens."),
		"workspace_language": stringProp("Registered LSP manager selector for workspace_symbol. Pass exactly one of workspace_language or file_path."),
		"match_mode":         enumProp("Workspace symbol match mode; exact is the default and fuzzy must be explicit", "exact", "fuzzy"),
		"language_id":        stringProp("Optional language server override for extensionless or ambiguous file_path values; invalid without file_path."),
		"max_results":        integerProp("Max results (default 20, cap 50)"),
		"work_dir":           lspWorkDirProp(),
	}, "action"),
		actionCondition("document_symbol", requiredWithForbiddenFields([]string{"file_path"}, "workspace_language")),
		actionCondition("workspace_symbol", workspaceSymbol),
		actionCondition("folding_range", requiredWithForbiddenFields([]string{"file_path"}, "workspace_language")),
		actionCondition("semantic_tokens", requiredWithForbiddenFields([]string{"file_path"}, "workspace_language")),
	)
}

// requiredWithForbiddenFields 组合 action 的必填字段与禁止字段约束。
func requiredWithForbiddenFields(required []string, forbidden ...string) schema {
	condition := forbidFields(forbidden...)
	condition["required"] = required
	return condition
}

func newPatchEditSchema() schema {
	replaceRange := forbidFields("pos", "new_name", "only")
	replaceRange["required"] = []string{"file_path", "patch"}
	rename := forbidFields("file_path", "patch", "only")
	rename["required"] = []string{"pos", "new_name"}
	codeAction := forbidFields("file_path", "patch", "new_name")
	codeAction["required"] = []string{"pos"}
	format := forbidFields("pos", "patch", "new_name", "only")
	format["required"] = []string{"file_path"}
	return withActionConditions(NewObjectSchema(map[string]schema{
		"action":      enumProp("Action.", "replace_range", "rename", "code_action", "format"),
		"file_path":   stringProp("File path (absolute or relative, auto-resolved). Required for replace_range and format."),
		"patch":       stringProp("Patch body for replace_range. Supports multi-section edits."),
		"pos":         stringProp("Position as 'file_path:line:column' for rename/code_action (example internal/foo.go:42:9)."),
		"new_name":    stringProp("New symbol name (rename only)."),
		"only":        arrayOfStringsProp("Code action kinds filter (code_action only, e.g. [\"quickfix\", \"refactor\"])."),
		"language_id": stringProp("Optional language server override for extensionless or ambiguous files."),
		"work_dir":    lspWorkDirProp(),
	}, "action"),
		actionCondition("replace_range", replaceRange),
		actionCondition("rename", rename),
		actionCondition("code_action", codeAction),
		actionCondition("format", format),
	)
}

func newLSPCompletionSchema() schema {
	return NewObjectSchema(map[string]schema{
		"pos":         stringProp("Position as 'file_path:line:column' (example internal/foo.go:42:9)"),
		"language_id": stringProp("Optional language server override for extensionless or ambiguous files."),
		"max_results": integerProp("Max candidates (default 20, cap 50)"),
		"work_dir":    lspWorkDirProp(),
	}, "pos")
}

// lspWorkDirProp 生成 work_dir 属性 schema。
// work_dir 会被当作本次工具调用的可信工作区根，路径解析必须落在该根下。
func lspWorkDirProp() schema {
	return stringProp("Explicit working directory for this tool call. Absolute paths are accepted as the call's trusted workspace root; relative work_dir paths resolve against the current trusted CWD, and relative tool paths resolve under it.")
}
