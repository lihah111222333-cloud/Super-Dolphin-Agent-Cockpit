package workspace

import (
	"encoding/json"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

// EventHeader 是 workspace 事件的公共头。
// Timestamp 由发布端填充，消费端用它排序或展示事件时间，不承载持久化版本号。
type EventHeader struct {
	Timestamp time.Time `json:"timestamp"`
}

// WorkspaceRunHeader 标识某个 workspace run 事件。
// DagKey 可为空以兼容纯 workspace 场景，RunKey 是事件分发和 UI 定位的稳定键。
type WorkspaceRunHeader struct {
	EventHeader
	DagKey string `json:"dag_key,omitempty"`
	RunKey string `json:"run_key"`
}

// WorkspaceRunCreated 表示 workspace run 已创建并写入持久化状态。
// Metadata 保持 raw JSON，避免事件层理解上游工具或 DAG 的私有字段。
type WorkspaceRunCreated struct {
	WorkspaceRunHeader
	ID            int64           `json:"id,omitempty"`
	SourceRoot    string          `json:"source_root"`
	WorkspacePath string          `json:"workspace_path"`
	Status        string          `json:"status,omitempty"`
	CreatedBy     string          `json:"created_by,omitempty"`
	UpdatedBy     string          `json:"updated_by,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	CreatedAt     time.Time       `json:"created_at,omitempty"`
	UpdatedAt     time.Time       `json:"updated_at,omitempty"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
}

// WorkspaceRunStatusChanged 表示 workspace run 状态发生转换。
// OldStatus 可为空，消费者不能仅凭它判断合法状态机，最终状态以 NewStatus 为准。
type WorkspaceRunStatusChanged struct {
	WorkspaceRunHeader
	OldStatus string `json:"old_status,omitempty"`
	NewStatus string `json:"new_status"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// WorkspaceRunMerged 汇报 workspace merge 的摘要。
// DryRun=true 时只表示模拟结果，不能被消费者当作源目录已经写入成功。
type WorkspaceRunMerged struct {
	WorkspaceRunHeader
	SourceRoot      string `json:"source_root"`
	WorkspacePath   string `json:"workspace_path"`
	Status          string `json:"status,omitempty"`
	DryRun          bool   `json:"dry_run,omitempty"`
	MergedFileCount int    `json:"merged_file_count,omitempty"`
	Removed         int    `json:"removed,omitempty"`
	Conflicts       int    `json:"conflicts,omitempty"`
	Unchanged       int    `json:"unchanged,omitempty"`
	Errors          int    `json:"errors,omitempty"`
	UpdatedBy       string `json:"updated_by,omitempty"`
}

// WorkspaceRunAborted 表示 workspace run 被标记中止。
// Reason 只用于展示和审计，状态落库是否成功由发布前的服务调用保证。
type WorkspaceRunAborted struct {
	WorkspaceRunHeader
	SourceRoot    string `json:"source_root,omitempty"`
	WorkspacePath string `json:"workspace_path,omitempty"`
	Status        string `json:"status,omitempty"`
	Reason        string `json:"reason,omitempty"`
	UpdatedBy     string `json:"updated_by,omitempty"`
}

// WorkspaceRunMergeError 汇报 merge 无法干净完成的结果。
// Conflicts/Errors 给 UI 快速分流处理，Message 保留服务层更具体的失败说明。
type WorkspaceRunMergeError struct {
	WorkspaceRunHeader
	SourceRoot    string `json:"source_root"`
	WorkspacePath string `json:"workspace_path"`
	Conflicts     int    `json:"conflicts,omitempty"`
	Errors        int    `json:"errors,omitempty"`
	Message       string `json:"message,omitempty"`
	UpdatedBy     string `json:"updated_by,omitempty"`
}

// Type 返回 workspace run 创建事件的分发编号。
func (WorkspaceRunCreated) Type() uint32 { return shared.EventTypeWorkspaceRunCreated }

// Type 返回 workspace run 状态变更事件的分发编号。
func (WorkspaceRunStatusChanged) Type() uint32 { return shared.EventTypeWorkspaceRunStatusChanged }

// Type 返回 workspace run merge 成功或 dry-run 摘要事件的分发编号。
func (WorkspaceRunMerged) Type() uint32 { return shared.EventTypeWorkspaceRunMerged }

// Type 返回 workspace run 中止事件的分发编号。
func (WorkspaceRunAborted) Type() uint32 { return shared.EventTypeWorkspaceRunAborted }

// Type 返回 workspace run merge 错误事件的分发编号。
func (WorkspaceRunMergeError) Type() uint32 { return shared.EventTypeWorkspaceRunMergeError }
