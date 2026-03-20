package contract

// ApprovalResponder handles tool call approval responses.
// Implemented by platform/rpc.ApprovalManager so module/turn depends only on this contract.
type ApprovalResponder interface {
	Respond(callID string, requestID *int64, decision ApprovalDecision) error
}

// ApprovalDecision captures the result of a tool-call approval.
type ApprovalDecision struct {
	Approved bool
	Reason   string
}
