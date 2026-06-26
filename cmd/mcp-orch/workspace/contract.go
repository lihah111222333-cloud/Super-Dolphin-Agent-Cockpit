package workspace

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	storeworkspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/workspace"
)

// Service 定义 workspace run 的服务边界。
// 实现负责持久化 run/file 状态，并在 merge 时处理源目录与工作区文件安全。
type Service interface {
	CreateRun(ctx context.Context, req CreateRunRequest) (*Run, error)
	GetRun(ctx context.Context, runKey string) (*Run, error)
	ListRuns(ctx context.Context, status, dagKey string, limit int) ([]Run, error)
	MergeRun(ctx context.Context, req MergeRunRequest) (*MergeRunResult, error)
	AbortRun(ctx context.Context, runKey, updatedBy, reason string) error
	ListRunFiles(ctx context.Context, runKey, state string) ([]RunFile, error)
	GetRunFile(ctx context.Context, runKey, path string) (*RunFile, error)
}

// Run 是 store 层 workspace run 的领域别名。
type Run = storeworkspace.WorkspaceRun

// RunFile 是 store 层 workspace run file 的领域别名。
type RunFile = storeworkspace.WorkspaceRunFile

// CreateRunRequest 是创建 workspace run 的服务层请求。
// SourceRoot 必填；WorkspacePath 为空时服务会在 .workspace/<runKey> 下创建隔离目录。
type CreateRunRequest struct {
	RunKey             string          `json:"run_key,omitempty"`
	DagKey             string          `json:"dag_key,omitempty"`
	SourceRoot         string          `json:"source_root"`
	WorkspacePath      string          `json:"workspace_path,omitempty"`
	CWD                string          `json:"cwd,omitempty"`
	Status             string          `json:"status,omitempty"`
	CreatedBy          string          `json:"created_by,omitempty"`
	UpdatedBy          string          `json:"updated_by,omitempty"`
	Files              []string        `json:"files,omitempty"`
	Metadata           json.RawMessage `json:"metadata,omitempty"`
	FinishedAt         *time.Time      `json:"finished_at,omitempty"`
	AllowedSourceRoots []string        `json:"-"`
}

// MergeRunRequest 是从 workspace 合并回 source root 的写入请求。
// DryRun 只评估不写文件，DeleteRemoved 控制是否允许安全删除源文件。
type MergeRunRequest struct {
	RunKey             string   `json:"run_key"`
	UpdatedBy          string   `json:"updated_by,omitempty"`
	DryRun             bool     `json:"dry_run,omitempty"`
	DeleteRemoved      bool     `json:"delete_removed,omitempty"`
	AllowedSourceRoots []string `json:"-"`
}

// MergeFileResult 记录单个文件的 merge 判定结果。
// Action/Reason 来自服务层冲突检测，调用方不能把缺失项当作已成功写入。
type MergeFileResult struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

// MergeRunResult 汇总一次 workspace merge 的状态和文件结果。
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

// UnmarshalJSON 同时接受 snake_case 和旧 camelCase workspace run 字段。
// 当前字段优先，旧字段只在当前字段为空时补齐，避免覆盖新调用方显式值。
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

// mergeLegacyCreateRunRequest 合并旧 camelCase 创建请求字段。
// snake_case 当前字段优先，旧字段只补空值，避免旧客户端覆盖新客户端显式输入。
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

// coalesceString 在当前字段为空时使用旧字段。
func coalesceString(current, legacy string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return strings.TrimSpace(legacy)
}
