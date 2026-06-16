package workspace

import (
	"context"
	"encoding/json"
	"time"
)

// Store defines persistence operations used by the workspace package.
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

// ListRunsFilter carries input for workspace operations.
type ListRunsFilter struct {
	Status string
	DagKey string
	Limit  int32
}

// UpdateRunStatusInput carries input for workspace operations.
type UpdateRunStatusInput struct {
	RunKey    string
	Status    string
	UpdatedBy string
	Metadata  json.RawMessage
}

// TransitionRunStatusInput carries input for workspace operations.
type TransitionRunStatusInput struct {
	RunKey     string
	FromStatus string
	Status     string
	UpdatedBy  string
	Metadata   json.RawMessage
}

// ListFilesFilter carries input for workspace operations.
type ListFilesFilter struct {
	RunKey string
	State  string
	Limit  int32
}

// WorkspaceRun describes a workspace API type.
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

// WorkspaceRunFile describes a workspace API type.
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
