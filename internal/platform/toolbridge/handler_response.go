package toolbridge

import (
	"encoding/json"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

const recoveryFailureResultText = "Recovery action is required. Sensitive diagnostics remain preserved internally."

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

// toolCallErrorResultWithMeta attaches trusted protocol metadata without exposing
// it in the model-visible text content.
func toolCallErrorResultWithMeta(text string, meta json.RawMessage) *ToolCallResult {
	result := toolCallErrorResult(text)
	if len(meta) == 0 || len(result.ContentItems) == 0 {
		return result
	}
	result.ContentItems[0].Meta = append(json.RawMessage(nil), meta...)
	return result
}

// toolCallRecoveryFailureResult 仅把显式 carrier 转成 provider 可见的四字段安全结果。
func toolCallRecoveryFailureResult(err error) (*ToolCallResult, bool) {
	failure, ok := contract.RecoveryFailureFromError(err)
	if !ok {
		return nil, false
	}
	structured, marshalErr := json.Marshal(failure)
	if marshalErr != nil {
		return nil, false
	}
	result := toolCallErrorResult(recoveryFailureResultText)
	result.StructuredContent = structured
	return result, true
}
