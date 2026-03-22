package mcp

import "encoding/json"

// ApprovalResponse is the response for ctl/approval/request.
type ApprovalResponse struct {
	Approved       *bool           `json:"approved,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	Detail         json.RawMessage `json:"detail,omitempty"`
	DecisionSource string          `json:"decision_source,omitempty"`
}
