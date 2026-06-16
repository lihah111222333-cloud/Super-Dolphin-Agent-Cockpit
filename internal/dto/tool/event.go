package tool

import (
	"time"

	shared "github.com/anthropic-ai/super-agent-v3/internal/dto/eventcore"
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
	Success       bool   `json:"success"`
	Error         string `json:"error,omitempty"`
	Result        string `json:"result,omitempty"`
	PersistedPath string `json:"persisted_path,omitempty"`
	Truncated     bool   `json:"truncated,omitempty"`
	OriginalSize  int    `json:"original_size,omitempty"`
	ElapsedMS     int64  `json:"elapsed_ms,omitempty"`
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

// Type 返回事件分发用的类型编号。
func (ToolCallBegin) Type() uint32 { return shared.EventTypeToolCallBegin }

// Type 返回事件分发用的类型编号。
func (ToolCallEnd) Type() uint32 { return shared.EventTypeToolCallEnd }

// Type 返回事件分发用的类型编号。
func (ToolApprovalRequested) Type() uint32 { return shared.EventTypeToolApprovalRequested }

// Type 返回事件分发用的类型编号。
func (ToolApprovalResolved) Type() uint32 { return shared.EventTypeToolApprovalResolved }

// Type 返回事件分发用的类型编号。
func (ToolDiffUpdated) Type() uint32 { return shared.EventTypeToolDiffUpdated }
