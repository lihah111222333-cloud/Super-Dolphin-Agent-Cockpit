package mcp

// 标准协议错误码：-326xx 沿用 JSON-RPC 规范，4xxx 为 MCP 自定义业务码。
const (
	ErrCodeInternal            = -32603 // JSON-RPC 内部错误。
	ErrCodeInvalidParams       = -32602 // JSON-RPC 参数非法。
	ErrCodeLeaseNotFound       = 4101   // 租约不存在。
	ErrCodeLeaseStale          = 4102   // 租约世代已过期。
	ErrCodeCapabilityMismatch  = 4103   // 能力协商不匹配。
	ErrCodeScopeNotAllowed     = 4104   // 请求 scope 未被授权。
	ErrCodeApprovalUnavailable = 4105   // 审批服务不可用。
	ErrCodePersistFailed       = 4106   // 持久化失败。
	ErrCodePeerUnavailable     = 4107   // 目标 peer 不可达。
	ErrCodeAuthFailed          = 4108   // 鉴权失败。
	ErrCodeBusy                = 4109   // 服务繁忙，建议稍后重试。
	ErrCodeTimeout             = 4110   // 操作超时。

	// ErrCodeReportConflict 是已废弃的别名，保留以让下游包平滑迁移，勿在新代码中使用。
	ErrCodeReportConflict = ErrCodePersistFailed
)
