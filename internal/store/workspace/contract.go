package workspace

import (
	"context"
	"encoding/json"
	"time"
)

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

type ListRunsFilter struct {
	Status string
	DagKey string
	Limit  int32
}

type UpdateRunStatusInput struct {
	RunKey    string
	Status    string
	UpdatedBy string
	Metadata  json.RawMessage
}

type TransitionRunStatusInput struct {
	RunKey     string
	FromStatus string
	Status     string
	UpdatedBy  string
	Metadata   json.RawMessage
}

type ListFilesFilter struct {
	RunKey string
	State  string
	Limit  int32
}

type WorkspaceRun struct {
	ID            int64
	RunKey        string
	DagKey        string
	SourceRoot    string
	WorkspacePath string
	Status        string
	CreatedBy     string
	UpdatedBy     string
	Metadata      json.RawMessage
	CreatedAt     time.Time
	UpdatedAt     time.Time
	FinishedAt    *time.Time
}

type WorkspaceRunFile struct {
	ID                 int64
	RunKey             string
	RelativePath       string
	BaselineSHA256     string
	WorkspaceSHA256    string
	SourceSHA256Before string
	SourceSHA256After  string
	State              string
	LastError          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}
