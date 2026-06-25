package mcp

import "encoding/json"

// ApprovalResponse 是 ctl/approval/request 的审批结果响应。
// Approved 为 nil 表示决策尚未落地（超时或待处理）。
type ApprovalResponse struct {
	Approved       *bool           `json:"approved,omitempty"`        // nil=未决，true=批准，false=拒绝。
	Reason         string          `json:"reason,omitempty"`          // 人工或自动决策的说明文本。
	Detail         json.RawMessage `json:"detail,omitempty"`          // 扩展详情，结构由调用方约定。
	DecisionSource string          `json:"decision_source,omitempty"` // 决策来源，见 DecisionSource* 常量。
}
