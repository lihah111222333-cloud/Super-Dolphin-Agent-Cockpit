package contract

import (
	"context"
	"encoding/json"
)

// ApprovalResponder handles tool call approval responses.
// Implemented by platform/rpc.ApprovalManager so module/turn depends only on this contract.
type ApprovalResponder interface {
	Respond(callID string, requestID *int64, decision ApprovalDecision) error
}

// ApprovalRequester requests a user approval decision.
type ApprovalRequester interface {
	RequestApproval(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error)
}

// ApprovalRequest captures the common approval request payload shared across modules.
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

// ApprovalDecision captures the result of a tool-call approval.
type ApprovalDecision struct {
	Approved *bool           `json:"approved,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	Detail   json.RawMessage `json:"detail,omitempty"`
}
