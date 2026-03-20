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
	MergeRun(ctx context.Context, req MergeRunRequest) (*MergeRunResult, error)
	AbortRun(ctx context.Context, runKey, updatedBy, reason string) error
	ListRunFiles(ctx context.Context, runKey, state string) ([]RunFile, error)
	GetRunFile(ctx context.Context, runKey, path string) (*RunFile, error)
}

type Run = storeworkspace.WorkspaceRun
type RunFile = storeworkspace.WorkspaceRunFile

type CreateRunRequest struct {
	RunKey        string          `json:"runKey,omitempty"`
	DagKey        string          `json:"dagKey,omitempty"`
	SourceRoot    string          `json:"sourceRoot"`
	WorkspacePath string          `json:"workspacePath,omitempty"`
	CWD           string          `json:"cwd,omitempty"`
	Status        string          `json:"status,omitempty"`
	CreatedBy     string          `json:"createdBy,omitempty"`
	UpdatedBy     string          `json:"updatedBy,omitempty"`
	Files         []string        `json:"files,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	FinishedAt    *time.Time      `json:"finishedAt,omitempty"`
}

type MergeRunRequest struct {
	RunKey        string `json:"runKey"`
	UpdatedBy     string `json:"updatedBy,omitempty"`
	DryRun        bool   `json:"dryRun,omitempty"`
	DeleteRemoved bool   `json:"deleteRemoved,omitempty"`
}

type MergeFileResult struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

type MergeRunResult struct {
	RunKey        string            `json:"runKey"`
	Status        string            `json:"status"`
	SourceRoot    string            `json:"sourceRoot"`
	WorkspacePath string            `json:"workspacePath"`
	DryRun        bool              `json:"dryRun"`
	Merged        int               `json:"merged"`
	Conflicts     int               `json:"conflicts"`
	Unchanged     int               `json:"unchanged"`
	Errors        int               `json:"errors"`
	Files         []MergeFileResult `json:"files,omitempty"`
}
