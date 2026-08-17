//go:build !windows

package toolbridge

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
)

// windowsACLApprovalRequest 在非 Windows 宿主上保持严格无操作；即使 peer
// 伪造 Windows 元数据，也不会改变原工具结果或触发 UI 审批。
func (h *Handler) windowsACLApprovalRequest(_ *mcpcontrol.ToolInstance, _ ToolCallRequest, _ *ToolCallResult) (contract.ApprovalRequest, bool) {
	return contract.ApprovalRequest{}, false
}

// logWindowsACLApprovalDecision 是非 Windows 编译面的空实现；调用门禁永远
// 返回 false，因此产品运行时不会到达这里。
func (h *Handler) logWindowsACLApprovalDecision(_ contract.ApprovalRequest, _ contract.ApprovalDecision, _ error) {
}
