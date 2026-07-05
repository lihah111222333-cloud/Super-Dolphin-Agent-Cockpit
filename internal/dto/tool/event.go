package tool

import (
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

// ToolCallBegin 报告一次工具调用开始，ArgumentsPreview 仅用于观测摘要。
type ToolCallBegin struct {
	shared.ToolCallHeader
	RequestID        int64  `json:"request_id,omitempty"`
	ArgumentsPreview string `json:"arguments_preview,omitempty"`
}

// ToolCallEnd 报告一次工具调用结束，包含成功状态、结果摘要和持久化产物路径。
type ToolCallEnd struct {
	shared.ToolCallHeader
	Success       bool   `json:"success"`
	Error         string `json:"error,omitempty"`
	Result        string `json:"result,omitempty"`
	PersistedPath string `json:"persisted_path,omitempty"`
	PersistFailed bool   `json:"persist_failed,omitempty"`
	PersistError  string `json:"persist_error,omitempty"`
	Truncated     bool   `json:"truncated,omitempty"`
	OriginalSize  int    `json:"original_size,omitempty"`
	ElapsedMS     int64  `json:"elapsed_ms,omitempty"`
}

// ToolApprovalRequested 报告工具调用进入审批等待状态。
type ToolApprovalRequested struct {
	shared.ToolApprovalHeader
	RequestID int64  `json:"request_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Kind      string `json:"kind,omitempty"`
}

// ToolApprovalResolved 报告工具调用审批已给出最终决策。
type ToolApprovalResolved struct {
	shared.ToolApprovalHeader
	Approved   bool   `json:"approved"`
	Decision   string `json:"decision,omitempty"`
	ReviewedBy string `json:"reviewed_by,omitempty"`
	Kind       string `json:"kind,omitempty"`
}

// ToolDiffUpdated 报告从已完成工具调用中提取出的 diff 更新。
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

// Type 返回事件总线使用的稳定类型编号，保持工具调用开始事件可路由。
func (ToolCallBegin) Type() uint32 { return shared.EventTypeToolCallBegin }

// Type 返回事件总线使用的稳定类型编号，保持工具调用结束事件可路由。
func (ToolCallEnd) Type() uint32 { return shared.EventTypeToolCallEnd }

// Type 返回事件总线使用的稳定类型编号，保持工具审批请求事件可路由。
func (ToolApprovalRequested) Type() uint32 { return shared.EventTypeToolApprovalRequested }

// Type 返回事件总线使用的稳定类型编号，保持工具审批结果事件可路由。
func (ToolApprovalResolved) Type() uint32 { return shared.EventTypeToolApprovalResolved }

// Type 返回事件总线使用的稳定类型编号，保持工具 diff 更新事件可路由。
func (ToolDiffUpdated) Type() uint32 { return shared.EventTypeToolDiffUpdated }
