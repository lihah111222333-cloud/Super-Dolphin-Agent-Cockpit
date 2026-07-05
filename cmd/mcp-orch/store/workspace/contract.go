package workspace

import (
	"context"
	"encoding/json"
	"time"
)

// Store 定义 workspace run 的持久化边界。
// 实现必须维护 run 与文件状态一致性，并通过 WithTx 包住跨表更新。
type Store interface {
	WithTx(ctx context.Context, fn func(txStore Store) error) error
	UpsertRun(ctx context.Context, run WorkspaceRun) (*WorkspaceRun, error)
	GetRun(ctx context.Context, runKey string) (*WorkspaceRun, error)
	ListRuns(ctx context.Context, filter ListRunsFilter) ([]WorkspaceRun, error)
	UpdateRunStatus(ctx context.Context, input UpdateRunStatusInput) (*WorkspaceRun, error)
	TransitionRunStatus(ctx context.Context, input TransitionRunStatusInput) (*WorkspaceRun, error)
	UpsertFile(ctx context.Context, file WorkspaceRunFile) (*WorkspaceRunFile, error)
	GetFile(ctx context.Context, runKey, relativePath string) (*WorkspaceRunFile, error)
	ListFiles(ctx context.Context, filter ListFilesFilter) ([]WorkspaceRunFile, error)
}

// ListRunsFilter 描述 workspace run 列表查询条件；Limit 为调用方传入的行数上限。
type ListRunsFilter struct {
	Status string
	DagKey string
	Limit  int32
}

// UpdateRunStatusInput 描述无前置状态检查的 run 状态写入参数。
type UpdateRunStatusInput struct {
	RunKey    string
	Status    string
	UpdatedBy string
	Metadata  json.RawMessage
}

// TransitionRunStatusInput 描述带 FromStatus CAS 约束的状态迁移，避免并发写覆盖。
type TransitionRunStatusInput struct {
	RunKey     string
	FromStatus string
	Status     string
	UpdatedBy  string
	Metadata   json.RawMessage
}

// ListFilesFilter 描述 run 文件列表查询条件；State 为空时不按文件状态过滤。
type ListFilesFilter struct {
	RunKey string
	State  string
	Limit  int32
}

// WorkspaceRun 是 workspace_runs 的领域视图，记录源目录、工作区路径和 run 生命周期。
type WorkspaceRun struct {
	ID            int64           `json:"id"`
	RunKey        string          `json:"run_key"`
	DagKey        string          `json:"dag_key,omitempty"`
	SourceRoot    string          `json:"source_root"`
	WorkspacePath string          `json:"workspace_path"`
	Status        string          `json:"status"`
	CreatedBy     string          `json:"created_by,omitempty"`
	UpdatedBy     string          `json:"updated_by,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
}

// WorkspaceRunFile 是 workspace_run_files 的领域视图，记录单文件 hash 与 merge 状态。
type WorkspaceRunFile struct {
	ID                 int64     `json:"id"`
	RunKey             string    `json:"run_key"`
	RelativePath       string    `json:"relative_path"`
	BaselineSHA256     string    `json:"baseline_sha256,omitempty"`
	WorkspaceSHA256    string    `json:"workspace_sha256,omitempty"`
	SourceSHA256Before string    `json:"source_sha256_before,omitempty"`
	SourceSHA256After  string    `json:"source_sha256_after,omitempty"`
	State              string    `json:"state"`
	LastError          string    `json:"last_error,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}
