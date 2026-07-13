package contract

import (
	"context"
	"encoding/json"
)

// ApprovalResponder 是 tool call 审批结果回写边界。
// turn 模块只依赖该接口，具体请求状态和 RPC 通知由 platform/rpc 实现。
type ApprovalResponder interface {
	Respond(identity ApprovalIdentity, decision ApprovalDecision) error
}

// ApprovalIdentity 是一次可操作审批的完整身份。
// SessionScope 由后端会话签发，CallID/RequestID 来自对应 provider 调用；三者缺一不可。
type ApprovalIdentity struct {
	SessionScope string `json:"sessionScope"`
	CallID       string `json:"callId"`
	RequestID    int64  `json:"requestId"`
}

// ApprovalRequester 发起需要用户裁决的审批请求。
// 实现负责阻塞等待或返回拒绝/取消，调用方不得自行伪造默认通过结果。
type ApprovalRequester interface {
	RequestApproval(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error)
}

// ApprovalRequest 是跨模块共享的审批请求 wire 载荷。
// CallID/ApprovalID 定位一次审批，Payload 保存工具相关上下文但不承诺具体 schema。
type ApprovalRequest struct {
	CallID       string
	ApprovalID   string
	ToolName     string
	AgentID      string
	ThreadID     string
	TurnID       string
	Reason       string
	Kind         string
	SourceMethod string
	Payload      map[string]any
}

// ApprovalDecision 是一次审批的最终裁决。
// Approved 为 nil 表示尚未形成裁决或被取消，调用方必须显式处理。
type ApprovalDecision struct {
	Approved *bool           `json:"approved,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	Detail   json.RawMessage `json:"detail,omitempty"`
}

// ArtifactApprovalRequest 定位一次 skill artifact 审批查询。
// prompt/catalog 通过该结构查询审批缓存，不依赖具体 skill approval 存储实现。
type ArtifactApprovalRequest struct {
	RepoFingerprint string
	Name            string
	ArtifactKind    string
	ArtifactLocator string
	ContentHash     string
}

// ApprovalSource 暴露只读 artifact 审批状态和单调递增 revision。
// prompt 缓存用 revision 判断是否需要失效重算。
type ApprovalSource interface {
	LookupArtifactApproval(ctx context.Context, req ArtifactApprovalRequest) (bool, error)
	ApprovalRevision() uint64
}
