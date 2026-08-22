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

func newLSPDiagnosticsSchema() schema {
	return schema{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"file_path":   map[string]any(stringProp("Single file path to diagnose. Pass exactly one of file_path or file_paths.")),
			"file_paths":  map[string]any(arrayOfStringsProp("Files to diagnose. Pass exactly one of file_path or file_paths.")),
			"language_id": map[string]any(stringProp("Optional language server override for extensionless or ambiguous files.")),
			"work_dir":    map[string]any(lspWorkDirProp()),
		},
		"oneOf": exactlyOneRequired("file_path", "file_paths"),
	}
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

// lspWorkDirProp 生成 work_dir 属性 schema。
// work_dir 会被当作本次工具调用的可信工作区根，路径解析必须落在该根下。
func lspWorkDirProp() schema {
	return stringProp("Explicit working directory for this tool call. Absolute paths are accepted as the call's trusted workspace root; relative work_dir paths resolve against the current trusted CWD, and relative tool paths resolve under it.")
}
