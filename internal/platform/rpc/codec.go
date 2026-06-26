package rpc

// PayloadEncoder 统一应用层 RPC payload 的成功和错误包裹格式。
type PayloadEncoder struct{}

// WrapSuccess 构造 success=true 的返回 payload，data 为空时不写入 data 字段。
func (e *PayloadEncoder) WrapSuccess(data any) map[string]any {
	payload := map[string]any{"success": true}
	if data != nil {
		payload["data"] = data
	}
	return payload
}

// WrapError 构造 success=false 的错误 payload，保留业务 code 和用户可读消息。
func (e *PayloadEncoder) WrapError(code int, msg string) map[string]any {
	return map[string]any{
		"success": false,
		"error": map[string]any{
			"code":    code,
			"message": msg,
		},
	}
}
