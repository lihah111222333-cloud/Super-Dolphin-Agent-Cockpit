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
			Hint: "next: grep action=text_search query=<query> path=<path> glob=<glob> max_results=10",
			NextAction: map[string]any{
				"tool":         "grep",
				"suggest_args": map[string]any{"max_results": 10},
				"tip":          "Scope search to a subdirectory or single file",
			},
		},
		"file": {
			Hint: "next: file action=read_file pos=<file>:<line> limit=100",
			NextAction: map[string]any{
				"tool":         "file",
				"suggest_args": map[string]any{"limit": 100},
				"tip":          "Read a specific range with offset and limit",
			},
		},
		"inspect": {
			Hint: "next: inspect pos=<file>:<line>:<col>",
		},
		"xref": {
			Hint: "next: xref max_results=10 and narrow pos/scope",
			NextAction: map[string]any{
				"tool":         "xref",
				"suggest_args": map[string]any{"max_results": 10},
			},
		},
		"structure": {
			Hint: "next: structure action=document_symbol file_path=<file> max_results=10",
		},
		"patch_edit": {
			Hint: "next: check success/applied fields for status",
		},
		"completion": {
			Hint: "next: completion pos=<file>:<line>:<col> max_results=10",
			NextAction: map[string]any{
				"tool":         "completion",
				"suggest_args": map[string]any{"max_results": 10},
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
	case "grep":
		s := map[string]any{
			"total":   numericField(payload, "total"),
			"showing": numericField(payload, "showing"),
		}
		if data, ok := payload["data"].(map[string]any); ok {
			names := make([]string, 0, 5)
			for k := range data {
				names = append(names, k)
				if len(names) >= 5 {
					break
				}
			}
			s["top_files"] = names
		}
		return s
	case "xref":
		return map[string]any{
			"total":   numericField(payload, "total"),
			"showing": numericField(payload, "showing"),
		}
	case "patch_edit":
		return map[string]any{
			"success": payload["success"],
			"applied": payload["applied"],
			"action":  payload["action"],
		}
	default:
		return map[string]any{}
	}
}
