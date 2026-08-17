package toolbridge

// schemaValidationAuthorizationDecision 是 schema validation 的跨平台控制 seam；
// 协议编排只消费 result/error/是否结束三项，Windows 审批细节由 tagged 实现提供，
// 非 Windows 实现严格 no-op，避免公共路径选择宿主权限行为。
type schemaValidationAuthorizationDecision struct {
	result         *ToolCallResult
	err            error
	validationDone bool
	handled        bool
}
