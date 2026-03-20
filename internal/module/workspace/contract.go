package workspace

import (
	"context"
	"encoding/json"
	"time"

	storeworkspace "github.com/anthropic-ai/super-agent-v3/internal/store/workspace"
)

type Service interface {
	CreateRun(ctx context.Context, req CreateRunRequest) (*Run, error)
	GetRun(ctx context.Context, runKey string) (*Run, error)
	ListRuns(ctx context.Context, status, dagKey string, limit int) ([]Run, error)
	UpdateRunStatus(ctx context.Context, runKey, status string) (*Run, error)
	MergeRun(ctx context.Context, runKey string) (*Run, error)
	AbortRun(ctx context.Context, runKey string) error
	ListRunFiles(ctx context.Context, runKey string) ([]RunFile, error)
	GetRunFile(ctx context.Context, runKey, path string) (*RunFile, error)
}

type Run = storeworkspace.WorkspaceRun
type RunFile = storeworkspace.WorkspaceRunFile

type CreateRunRequest struct {
	RunKey        string          `json:"runKey,omitempty"`
	DagKey        string          `json:"dagKey,omitempty"`
	SourceRoot    string          `json:"sourceRoot"`
	WorkspacePath string          `json:"workspacePath,omitempty"`
	Status        string          `json:"status,omitempty"`
	CreatedBy     string          `json:"createdBy,omitempty"`
	UpdatedBy     string          `json:"updatedBy,omitempty"`
	Files         []string        `json:"files,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	FinishedAt    *time.Time      `json:"finishedAt,omitempty"`
}
