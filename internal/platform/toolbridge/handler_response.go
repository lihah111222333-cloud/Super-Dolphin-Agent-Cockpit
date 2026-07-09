package toolbridge

// toolCallTextResult 构造文本型 ToolCallResult，供本地拦截和错误分支复用。
func toolCallTextResult(success bool, text string) *ToolCallResult {
	return &ToolCallResult{
		Success: success,
		ContentItems: []ToolCallContentItem{{
			Type: "inputText",
			Text: text,
		}},
	}
}

// toolCallErrorResult 构造失败文本结果，保持 peer callback 错误不向 JSON-RPC 外层冒泡。
func toolCallErrorResult(text string) *ToolCallResult {
	return toolCallTextResult(false, text)
}
