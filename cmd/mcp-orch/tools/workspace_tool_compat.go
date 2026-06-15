package tools

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	workspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/workspace"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type WorkspaceMergeFileResult struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

type WorkspaceMergeRunResult struct {
	RunKey        string                     `json:"run_key"`
	Status        string                     `json:"status"`
	SourceRoot    string                     `json:"source_root"`
	WorkspacePath string                     `json:"workspace_path"`
	WorkspaceRoot string                     `json:"workspace_root"`
	DryRun        bool                       `json:"dry_run"`
	DeleteRemoved bool                       `json:"delete_removed"`
	Merged        int                        `json:"merged"`
	FilesMerged   int                        `json:"files_merged"`
	Removed       int                        `json:"removed"`
	Conflicts     int                        `json:"conflicts"`
	Unchanged     int                        `json:"unchanged"`
	Errors        int                        `json:"errors"`
	FinishedAt    *time.Time                 `json:"finished_at,omitempty"`
	Files         []WorkspaceMergeFileResult `json:"files,omitempty"`
}

type workspaceRunDTO struct {
	ID            int64           `json:"id"`
	RunKey        string          `json:"run_key"`
	DagKey        string          `json:"dag_key,omitempty"`
	SourceRoot    string          `json:"source_root"`
	WorkspacePath string          `json:"workspace_path"`
	WorkspaceRoot string          `json:"workspace_root"`
	Status        string          `json:"status"`
	CreatedBy     string          `json:"created_by,omitempty"`
	UpdatedBy     string          `json:"updated_by,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
	Files         []string        `json:"files"`
}

func workspaceRunDTOFromRun(ctx context.Context, svc workspace.Service, run *workspace.Run) (*workspaceRunDTO, error) {
	if run == nil {
		return nil, nil
	}
	files, err := listWorkspaceRunFiles(ctx, svc, run.RunKey)
	if err != nil {
		return nil, err
	}
	return &workspaceRunDTO{
		ID:            run.ID,
		RunKey:        run.RunKey,
		DagKey:        run.DagKey,
		SourceRoot:    run.SourceRoot,
		WorkspacePath: run.WorkspacePath,
		WorkspaceRoot: run.WorkspacePath,
		Status:        run.Status,
		CreatedBy:     run.CreatedBy,
		UpdatedBy:     run.UpdatedBy,
		Metadata:      shared.CloneRawMessage(run.Metadata),
		CreatedAt:     run.CreatedAt,
		UpdatedAt:     run.UpdatedAt,
		FinishedAt:    shared.CloneTime(run.FinishedAt),
		Files:         files,
	}, nil
}

func mapWorkspaceRuns(ctx context.Context, svc workspace.Service, runs []workspace.Run) ([]workspaceRunDTO, error) {
	if len(runs) == 0 {
		return nil, nil
	}
	mapped := make([]workspaceRunDTO, 0, len(runs))
	for i := range runs {
		runDTO, err := workspaceRunDTOFromRun(ctx, svc, &runs[i])
		if err != nil {
			return nil, err
		}
		mapped = append(mapped, *runDTO)
	}
	return mapped, nil
}

func convertMergeResult(r *workspace.MergeRunResult, deleteRemoved bool) *WorkspaceMergeRunResult {
	if r == nil {
		return nil
	}
	files := make([]WorkspaceMergeFileResult, 0, len(r.Files))
	for _, file := range r.Files {
		files = append(files, WorkspaceMergeFileResult{
			Path:   file.Path,
			Action: file.Action,
			Reason: file.Reason,
		})
	}
	return &WorkspaceMergeRunResult{
		RunKey:        r.RunKey,
		Status:        r.Status,
		SourceRoot:    r.SourceRoot,
		WorkspacePath: r.WorkspacePath,
		WorkspaceRoot: r.WorkspacePath,
		DryRun:        r.DryRun,
		DeleteRemoved: deleteRemoved,
		Merged:        r.Merged,
		FilesMerged:   r.Merged,
		Removed:       r.Removed,
		Conflicts:     r.Conflicts,
		Unchanged:     r.Unchanged,
		Errors:        r.Errors,
		Files:         files,
	}
}

// listWorkspaceRunFiles 列出工作区运行记录文件。
func listWorkspaceRunFiles(ctx context.Context, svc workspace.Service, runKey string) ([]string, error) {
	if svc == nil || strings.TrimSpace(runKey) == "" {
		return []string{}, nil
	}
	runFiles, err := svc.ListRunFiles(ctx, runKey, "")
	if err != nil {
		return nil, err
	}
	if len(runFiles) == 0 {
		return []string{}, nil
	}
	files := make([]string, 0, len(runFiles))
	for _, file := range runFiles {
		path := strings.TrimSpace(file.RelativePath)
		if path != "" {
			files = append(files, path)
		}
	}
	sort.Strings(files)
	return files, nil
}
