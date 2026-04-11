package tool

import (
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

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
	Result    string `json:"result,omitempty"`
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

// ToolDiffUpdated reports a diff extracted from a completed tool invocation.
type ToolDiffUpdated struct {
	Timestamp time.Time `json:"timestamp"`
	ThreadID  string    `json:"threadId"`
	AgentID   string    `json:"agentId"`
	CallID    string    `json:"callId,omitempty"`
	ToolName  string    `json:"toolName,omitempty"`
	DiffText  string    `json:"diffText"`
	Files     []string  `json:"files"`
	Revision  int64     `json:"revision,omitempty"`
}

func (ToolCallBegin) Type() uint32         { return shared.EventTypeToolCallBegin }
func (ToolCallEnd) Type() uint32           { return shared.EventTypeToolCallEnd }
func (ToolApprovalRequested) Type() uint32 { return shared.EventTypeToolApprovalRequested }
func (ToolApprovalResolved) Type() uint32  { return shared.EventTypeToolApprovalResolved }
func (ToolDiffUpdated) Type() uint32       { return shared.EventTypeToolDiffUpdated }
