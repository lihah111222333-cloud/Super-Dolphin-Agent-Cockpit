package tools

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	workspace "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/workspace"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// WorkspaceMergeFileResult 是 workspace merge 单文件结果的工具层 DTO。
type WorkspaceMergeFileResult struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

// WorkspaceMergeRunResult 是 workspace_merge_run 的兼容响应。
// WorkspaceRoot 和 FilesMerged 保留旧 UI 字段，值来自 WorkspacePath 和 Merged。
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

// workspaceRunDTO 是 workspace run 暴露给 MCP 工具和 UI 的稳定形状。
// Files 需要从 run file 表补齐，不能只依赖 run 主表。
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

// workspaceRunDTOFromRun 将服务层 run 映射为工具层 DTO。
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

// mapWorkspaceRuns 批量映射 workspace run 列表。
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

// convertMergeResult 将服务层 merge 结果转换为工具层兼容响应。
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

// listWorkspaceRunFiles 为兼容 DTO 补齐工作区文件路径。
// svc 缺失或 run_key 为空时返回空列表，保持读取型 DTO 构造不越过服务边界。
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
