package contract

import "encoding/json"

// ApprovalResponder handles tool call approval responses.
// Implemented by platform/rpc.ApprovalManager so module/turn depends only on this contract.
type ApprovalResponder interface {
	Respond(callID string, requestID *int64, decision ApprovalDecision) error
}

// ApprovalDecision captures the result of a tool-call approval.
type ApprovalDecision struct {
	Approved *bool           `json:"approved,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	Detail   json.RawMessage `json:"detail,omitempty"`
}
