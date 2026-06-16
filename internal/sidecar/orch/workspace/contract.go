package workspace

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	storeworkspace "github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/workspace"
)

// Service provides workspace use-case operations.
type Service interface {
	CreateRun(ctx context.Context, req CreateRunRequest) (*Run, error)
	GetRun(ctx context.Context, runKey string) (*Run, error)
	ListRuns(ctx context.Context, status, dagKey string, limit int) ([]Run, error)
	MergeRun(ctx context.Context, req MergeRunRequest) (*MergeRunResult, error)
	AbortRun(ctx context.Context, runKey, updatedBy, reason string) error
	ListRunFiles(ctx context.Context, runKey, state string) ([]RunFile, error)
	GetRunFile(ctx context.Context, runKey, path string) (*RunFile, error)
}

// Run describes a workspace API type.
type Run = storeworkspace.WorkspaceRun

// RunFile describes a workspace API type.
type RunFile = storeworkspace.WorkspaceRunFile

// CreateRunRequest carries input for workspace operations.
type CreateRunRequest struct {
	RunKey        string          `json:"run_key,omitempty"`
	DagKey        string          `json:"dag_key,omitempty"`
	SourceRoot    string          `json:"source_root"`
	WorkspacePath string          `json:"workspace_path,omitempty"`
	CWD           string          `json:"cwd,omitempty"`
	Status        string          `json:"status,omitempty"`
	CreatedBy     string          `json:"created_by,omitempty"`
	UpdatedBy     string          `json:"updated_by,omitempty"`
	Files         []string        `json:"files,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
}

// MergeRunRequest carries input for workspace operations.
type MergeRunRequest struct {
	RunKey        string `json:"run_key"`
	UpdatedBy     string `json:"updated_by,omitempty"`
	DryRun        bool   `json:"dry_run,omitempty"`
	DeleteRemoved bool   `json:"delete_removed,omitempty"`
}

// MergeFileResult contains output returned by workspace operations.
type MergeFileResult struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

// MergeRunResult contains output returned by workspace operations.
type MergeRunResult struct {
	RunKey        string            `json:"run_key"`
	Status        string            `json:"status"`
	SourceRoot    string            `json:"source_root"`
	WorkspacePath string            `json:"workspace_path"`
	DryRun        bool              `json:"dry_run"`
	Merged        int               `json:"merged"`
	Removed       int               `json:"removed"`
	Conflicts     int               `json:"conflicts"`
	Unchanged     int               `json:"unchanged"`
	Errors        int               `json:"errors"`
	Files         []MergeFileResult `json:"files,omitempty"`
}

// UnmarshalJSON 解码JSON。
func (r *CreateRunRequest) UnmarshalJSON(data []byte) error {
	type raw CreateRunRequest
	var legacy struct {
		RunKey        string     `json:"runKey"`
		DagKey        string     `json:"dagKey"`
		SourceRoot    string     `json:"sourceRoot"`
		WorkspacePath string     `json:"workspacePath"`
		CreatedBy     string     `json:"createdBy"`
		UpdatedBy     string     `json:"updatedBy"`
		FinishedAt    *time.Time `json:"finishedAt"`
	}
	return decodeLegacyRunParams(data, func() error {
		var current raw
		if err := json.Unmarshal(data, &current); err != nil {
			return err
		}
		*r = CreateRunRequest(current)
		return nil
	}, &legacy, func(legacy struct {
		RunKey        string     `json:"runKey"`
		DagKey        string     `json:"dagKey"`
		SourceRoot    string     `json:"sourceRoot"`
		WorkspacePath string     `json:"workspacePath"`
		CreatedBy     string     `json:"createdBy"`
		UpdatedBy     string     `json:"updatedBy"`
		FinishedAt    *time.Time `json:"finishedAt"`
	}) {
		mergeLegacyCreateRunRequest(r, legacy)
	})
}

func mergeLegacyCreateRunRequest(r *CreateRunRequest, legacy struct {
	RunKey        string     `json:"runKey"`
	DagKey        string     `json:"dagKey"`
	SourceRoot    string     `json:"sourceRoot"`
	WorkspacePath string     `json:"workspacePath"`
	CreatedBy     string     `json:"createdBy"`
	UpdatedBy     string     `json:"updatedBy"`
	FinishedAt    *time.Time `json:"finishedAt"`
}) {
	r.RunKey = coalesceString(r.RunKey, legacy.RunKey)
	r.DagKey = coalesceString(r.DagKey, legacy.DagKey)
	r.SourceRoot = coalesceString(r.SourceRoot, legacy.SourceRoot)
	r.WorkspacePath = coalesceString(r.WorkspacePath, legacy.WorkspacePath)
	r.CreatedBy = coalesceString(r.CreatedBy, legacy.CreatedBy)
	r.UpdatedBy = coalesceString(r.UpdatedBy, legacy.UpdatedBy)
	if r.FinishedAt == nil && legacy.FinishedAt != nil {
		value := *legacy.FinishedAt
		r.FinishedAt = &value
	}
}

func coalesceString(current, legacy string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return strings.TrimSpace(legacy)
}
