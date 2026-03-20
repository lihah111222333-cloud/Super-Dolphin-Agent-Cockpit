package tool

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

// ToolCallBegin reports the start of a tool invocation.
type ToolCallBegin struct {
	shared.ToolCallHeader
	RequestID        int64  `json:"requestId,omitempty"`
	ArgumentsPreview string `json:"argumentsPreview,omitempty"`
}

// ToolCallEnd reports the end of a tool invocation.
type ToolCallEnd struct {
	shared.ToolCallHeader
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
	ElapsedMS int64  `json:"elapsedMs,omitempty"`
}

// ToolApprovalRequested reports a tool call waiting for approval.
type ToolApprovalRequested struct {
	shared.ToolApprovalHeader
	RequestID int64  `json:"requestId,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// ToolApprovalResolved reports a final approval decision for a tool call.
type ToolApprovalResolved struct {
	shared.ToolApprovalHeader
	Approved   bool   `json:"approved"`
	Decision   string `json:"decision,omitempty"`
	ReviewedBy string `json:"reviewedBy,omitempty"`
}

func (ToolCallBegin) Type() uint32         { return shared.EventTypeToolCallBegin }
func (ToolCallEnd) Type() uint32           { return shared.EventTypeToolCallEnd }
func (ToolApprovalRequested) Type() uint32 { return shared.EventTypeToolApprovalRequested }
func (ToolApprovalResolved) Type() uint32  { return shared.EventTypeToolApprovalResolved }
