package middleware

// toolOverflowHint 保存工具结果超预算时返回给调用方的提示和后续动作建议。
type toolOverflowHint struct {
	Hint       string
	NextAction map[string]any
}

// toolOverflowHints 构造工具溢出提示，用于引导调用方收窄查询。
func toolOverflowHints() map[string]toolOverflowHint {
	return map[string]toolOverflowHint{
		"grep": {
			Hint: "next: use native rg with a narrower path or query",
			NextAction: map[string]any{
				"tool": "grep",
			},
		},
		"structure": {
			Hint: "next: structure action=document_symbol file_path=<file> max_results=10",
		},
		"xref": {
			Hint: "next: xref max_results=10 and narrow pos/scope",
			NextAction: map[string]any{
				"tool":         "xref",
				"suggest_args": map[string]any{"max_results": 10},
			},
		},
		"diagnostics": {
			Hint: "next: split file_paths into smaller diagnostics batches",
			NextAction: map[string]any{
				"tool": "diagnostics",
			},
		},
	}
}

// lookupHint 根据工具名称选择溢出提示。
// 未命中具体工具时返回默认提示，避免调用方收到空建议。
func lookupHint(toolName string) toolOverflowHint {
	if h, ok := toolOverflowHints()[toolName]; ok {
		return h
	}
	return toolOverflowHint{Hint: "next: narrow the query"}
}

// extractSummary 从工具结果中提取可回显摘要。
// payload 为空或字段类型不匹配时返回空 map，由上层继续生成通用提示。
func extractSummary(toolName string, payload map[string]any) map[string]any {
	if payload == nil {
		return map[string]any{}
	}
	switch toolName {
	case "xref", "diagnostics":
		return map[string]any{
			"total":   numericField(payload, "total"),
			"showing": numericField(payload, "showing"),
		}
	default:
		return map[string]any{}
	}
}
