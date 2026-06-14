package rpc

// PayloadEncoder standardizes application-level RPC payload wrappers.
type PayloadEncoder struct{}

// WrapSuccess 包装success。
func (e *PayloadEncoder) WrapSuccess(data any) map[string]any {
	payload := map[string]any{"success": true}
	if data != nil {
		payload["data"] = data
	}
	return payload
}

// WrapError 包装错误。
func (e *PayloadEncoder) WrapError(code int, msg string) map[string]any {
	return map[string]any{
		"success": false,
		"error": map[string]any{
			"code":    code,
			"message": msg,
		},
	}
}
