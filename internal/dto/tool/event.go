package tool

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

// ToolCallBegin reports the start of a tool invocation.
type ToolCallBegin struct {
	shared.ToolCallHeader
	RequestID        int64  `json:"request_id,omitempty"`
	ArgumentsPreview string `json:"arguments_preview,omitempty"`
}

// ToolCallEnd reports the end of a tool invocation.
type ToolCallEnd struct {
	shared.ToolCallHeader
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms,omitempty"`
}

// ToolApprovalRequested reports a tool call waiting for approval.
type ToolApprovalRequested struct {
	shared.ToolApprovalHeader
	RequestID int64  `json:"request_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Kind      string `json:"kind,omitempty"`
}

// ToolApprovalResolved reports a final approval decision for a tool call.
type ToolApprovalResolved struct {
	shared.ToolApprovalHeader
	Approved   bool   `json:"approved"`
	Decision   string `json:"decision,omitempty"`
	ReviewedBy string `json:"reviewed_by,omitempty"`
	Kind       string `json:"kind,omitempty"`
}

func (ToolCallBegin) Type() uint32         { return shared.EventTypeToolCallBegin }
func (ToolCallEnd) Type() uint32           { return shared.EventTypeToolCallEnd }
func (ToolApprovalRequested) Type() uint32 { return shared.EventTypeToolApprovalRequested }
func (ToolApprovalResolved) Type() uint32  { return shared.EventTypeToolApprovalResolved }
