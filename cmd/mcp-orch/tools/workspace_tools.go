package tools

import (
	"context"
	"encoding/json"
	"strings"

	workspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/workspace"
	mcpcommon "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

// workspace 工具列表限制。
const (
	defaultWorkspaceListLimit = 200
	maxWorkspaceListLimit     = 5000
)

// WorkspaceCreateRunRequest 是 workspace_create_run 的 wire 入参。
// 工具层校验必填 shape 并裁剪可选字段；路径沙箱和复制安全仍由 workspace.Service 统一执行。
type WorkspaceCreateRunRequest struct {
	RunKey     string         `json:"run_key,omitempty"`
	DagKey     string         `json:"dag_key,omitempty"`
	SourceRoot string         `json:"source_root"`
	CreatedBy  string         `json:"created_by,omitempty"`
	Files      []string       `json:"files,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// workspaceGetRunInput 是 workspace_get_run 的入参。
// pos 是新定位符，run_key 保留给旧调用方。
type workspaceGetRunInput struct {
	RunKey string `json:"run_key"`
	Pos    string `json:"pos,omitempty"`
}

// workspaceListRunsInput 是 workspace_list_runs 的过滤条件。
// Envelope=true 返回分页对象；默认保留旧数组响应，避免破坏现有工具调用方。
type workspaceListRunsInput struct {
	Status   string `json:"status,omitempty"`
	DagKey   string `json:"dag_key,omitempty"`
	Pos      string `json:"pos,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Envelope bool   `json:"envelope,omitempty"`
}

// WorkspaceListRunsOutput 是 workspace_list_runs 的 envelope 响应。
// Runs 与 Data 指向同一批数据，兼容旧字段和通用列表控件。
type WorkspaceListRunsOutput struct {
	Runs      []workspaceRunDTO `json:"runs"`
	Data      []workspaceRunDTO `json:"data"`
	Total     int               `json:"total"`
	Showing   int               `json:"showing"`
	Truncated bool              `json:"truncated"`
	Hint      string            `json:"hint,omitempty"`
}

// WorkspaceMergeRunRequest 是 workspace_merge_run 的写入请求。
// DeleteRemoved 只有在 workspace 文件确实消失且 source 未漂移时才会删除源文件。
type WorkspaceMergeRunRequest struct {
	RunKey        string `json:"run_key"`
	UpdatedBy     string `json:"updated_by,omitempty"`
	DryRun        bool   `json:"dry_run,omitempty"`
	DeleteRemoved bool   `json:"delete_removed,omitempty"`
}

// workspaceAbortRunInput 是 workspace_abort_run 的入参。
type workspaceAbortRunInput struct {
	RunKey    string `json:"run_key"`
	UpdatedBy string `json:"updated_by,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// HandleWorkspaceCreateRun 创建可编辑的虚拟工作区运行。
func HandleWorkspaceCreateRun(svc workspace.Service) ToolHandler {
	return makeHandler(svc, "workspace service", func(ctx context.Context, in WorkspaceCreateRunRequest) (*workspaceRunDTO, error) {
		return createWorkspaceRun(ctx, svc, in)
	})
}

// HandleWorkspaceGetRun 读取单个 workspace run，并附带文件列表 DTO。
func HandleWorkspaceGetRun(svc workspace.Service) ToolHandler {
	return makeHandler(svc, "workspace service", func(ctx context.Context, in workspaceGetRunInput) (*workspaceRunDTO, error) {
		return getWorkspaceRun(ctx, svc, in)
	})
}

// HandleWorkspaceListRuns 列出 workspace run。
// 默认保持旧数组响应，Envelope=true 时返回分页包装对象给通用列表控件。
func HandleWorkspaceListRuns(svc workspace.Service) ToolHandler {
	return makeHandler(svc, "workspace service", func(ctx context.Context, in workspaceListRunsInput) (any, error) {
		runs, err := listWorkspaceRuns(ctx, svc, in)
		if err != nil {
			return nil, err
		}
		if in.Envelope {
			return newWorkspaceListRunsOutput(runs, normalizeWorkspaceListLimit(in.Limit)), nil
		}
		return runs, nil
	})
}

// HandleWorkspaceMergeRun 将 workspace 变更合并回 source root。
func HandleWorkspaceMergeRun(svc workspace.Service) ToolHandler {
	return makeHandler(svc, "workspace service", func(ctx context.Context, in WorkspaceMergeRunRequest) (*WorkspaceMergeRunResult, error) {
		return mergeWorkspaceRun(ctx, svc, in)
	})
}

// HandleWorkspaceAbortRun 标记 workspace run 已中止并返回最新 DTO。
func HandleWorkspaceAbortRun(svc workspace.Service) ToolHandler {
	return makeHandler(svc, "workspace service", func(ctx context.Context, in workspaceAbortRunInput) (*workspaceRunDTO, error) {
		return abortWorkspaceRun(ctx, svc, in)
	})
}

// workspaceToolDefinitions 注册 workspace 工具 schema 与 handler。
// 路径逃逸和源文件冲突仍由 workspace.Service 执行，工具层只做入参形状约束。
func workspaceToolDefinitions(svc workspace.Service) []ToolDefinition {
	return buildToolDefinitions(
		defineTool("workspace_create_run", "Create a virtual workspace run. Filesystem workspace is used for edits; run status and file states are stored in persistent state.", ObjectSchema(map[string]Schema{
			"run_key":     StringSchema("Optional run key. Auto-generated if omitted."),
			"dag_key":     StringSchema("Related DAG key (optional)."),
			"source_root": StringSchema("Absolute or relative source project root."),
			"created_by":  StringSchema("Creator identifier (optional)."),
			"files":       ArraySchema(StringSchema("Relative file path to copy into the workspace."), "Optional bootstrap files to copy from source root to workspace."),
			"metadata":    RawObjectSchema("Optional metadata for the run record."),
		}, "source_root"), HandleWorkspaceCreateRun(svc)),
		defineTool("workspace_get_run", "Get workspace run detail by run key.", ObjectSchema(map[string]Schema{
			"pos":     StringSchema("Flattened workspace run locator, e.g. workspace:<run_key>. Preferred over legacy run_key."),
			"run_key": StringSchema("Workspace run key."),
		}), HandleWorkspaceGetRun(svc)),
		defineTool("workspace_list_runs", "List workspace runs with optional status and DAG filters.", ObjectSchema(map[string]Schema{
			"status":   StringSchema("Optional run status filter."),
			"pos":      StringSchema("Optional flattened DAG locator for filtering, e.g. dag:<dag_key>. Preferred over legacy dag_key."),
			"dag_key":  StringSchema("Optional DAG key filter."),
			"limit":    IntegerSchema("Max number of runs to return."),
			"envelope": BooleanSchema("When true, return {runs,data,total,showing,truncated,hint}; default false keeps the legacy array response."),
		}), HandleWorkspaceListRuns(svc)),
		defineTool("workspace_merge_run", "Merge changed files from virtual workspace back to source root with conflict detection. Also updates persistent state for the run and file states.", ObjectSchema(map[string]Schema{
			"run_key":        StringSchema("Workspace run key."),
			"updated_by":     StringSchema("Operator identifier (optional)."),
			"dry_run":        BooleanSchema("Only simulate merge without writing source files."),
			"delete_removed": BooleanSchema("Delete source files removed in workspace when safe."),
		}, "run_key"), HandleWorkspaceMergeRun(svc)),
		defineTool("workspace_abort_run", "Abort a workspace run and mark it as aborted in persistent state.", ObjectSchema(map[string]Schema{
			"run_key":    StringSchema("Workspace run key."),
			"updated_by": StringSchema("Operator identifier (optional)."),
			"reason":     StringSchema("Abort reason (optional)."),
		}, "run_key"), HandleWorkspaceAbortRun(svc)),
	)
}

// createWorkspaceRun 在工具边界校验 source_root 并裁剪可选字段。
// 真正的路径安全、文件复制和持久化失败处理仍集中在 workspace.Service。
func createWorkspaceRun(ctx context.Context, svc workspace.Service, input WorkspaceCreateRunRequest) (*workspaceRunDTO, error) {
	if err := requireDependency(svc, "workspace service"); err != nil {
		return nil, err
	}
	sourceRoot, err := requireTrimmed(input.SourceRoot, "source_root")
	if err != nil {
		return nil, err
	}
	cwd, allowedRoots, err := workspaceCreateScope(ctx, sourceRoot)
	if err != nil {
		return nil, err
	}
	metadata, err := marshalMapToJSON(input.Metadata)
	if err != nil {
		return nil, err
	}
	run, err := svc.CreateRun(ctx, workspace.CreateRunRequest{
		RunKey:             strings.TrimSpace(input.RunKey),
		DagKey:             strings.TrimSpace(input.DagKey),
		SourceRoot:         sourceRoot,
		CWD:                cwd,
		CreatedBy:          strings.TrimSpace(input.CreatedBy),
		Files:              trimNonEmpty(input.Files),
		Metadata:           metadata,
		AllowedSourceRoots: allowedRoots,
	})
	if err != nil {
		return nil, err
	}
	return workspaceRunDTOFromRun(ctx, svc, run)
}

// getWorkspaceRun 解析 run 定位符并读取 DTO。
func getWorkspaceRun(ctx context.Context, svc workspace.Service, input workspaceGetRunInput) (*workspaceRunDTO, error) {
	if err := requireDependency(svc, "workspace service"); err != nil {
		return nil, err
	}
	runKey, err := resolveWorkspaceRunKeyInput(input.RunKey, input.Pos)
	if err != nil {
		return nil, err
	}
	run, err := svc.GetRun(ctx, runKey)
	run, err = loadOrNotFound(run, err, "workspace run", runKey)
	if err != nil {
		return nil, err
	}
	return workspaceRunDTOFromRun(ctx, svc, run)
}

// listWorkspaceRuns 解析可选 DAG pos 并映射为兼容 DTO 列表。
func listWorkspaceRuns(ctx context.Context, svc workspace.Service, input workspaceListRunsInput) ([]workspaceRunDTO, error) {
	if err := requireDependency(svc, "workspace service"); err != nil {
		return nil, err
	}
	dagKey, err := resolveOptionalDAGKeyInput(input.DagKey, input.Pos)
	if err != nil {
		return nil, err
	}
	runs, err := svc.ListRuns(ctx, strings.TrimSpace(input.Status), dagKey, normalizeWorkspaceListLimit(input.Limit))
	if err != nil {
		return nil, err
	}
	return mapWorkspaceRuns(ctx, svc, runs)
}

// newWorkspaceListRunsOutput 构造 workspace 列表分页响应。
func newWorkspaceListRunsOutput(runs []workspaceRunDTO, limit int) WorkspaceListRunsOutput {
	env := newListEnvelope(runs, limit, "next: use workspace_get_run pos=workspace:<run_key> for details")
	return WorkspaceListRunsOutput{
		Runs:      runs,
		Data:      env.Data,
		Total:     env.Total,
		Showing:   env.Showing,
		Truncated: env.Truncated,
		Hint:      env.Hint,
	}
}

// mergeWorkspaceRun 执行 workspace merge 并转换为工具层兼容响应。
// 非 dry-run 需要回读 run，补齐 service.MergeRunResult 中没有携带的 FinishedAt。
func mergeWorkspaceRun(ctx context.Context, svc workspace.Service, input WorkspaceMergeRunRequest) (*WorkspaceMergeRunResult, error) {
	if err := requireDependency(svc, "workspace service"); err != nil {
		return nil, err
	}
	runKey, err := requireTrimmed(input.RunKey, "run_key")
	if err != nil {
		return nil, err
	}
	allowedRoots, err := trustedWorkspaceRoots(ctx)
	if err != nil {
		return nil, err
	}
	result, err := svc.MergeRun(ctx, workspace.MergeRunRequest{
		RunKey:             runKey,
		UpdatedBy:          strings.TrimSpace(input.UpdatedBy),
		DryRun:             input.DryRun,
		DeleteRemoved:      input.DeleteRemoved,
		AllowedSourceRoots: allowedRoots,
	})
	if err != nil {
		return nil, err
	}
	out := convertMergeResult(result, input.DeleteRemoved)
	// workspace.Service 的 merge 摘要不携带 FinishedAt；非 dry-run 时回读持久化 run 补齐 UI 状态。
	if !input.DryRun {
		if run, runErr := svc.GetRun(ctx, runKey); runErr == nil && run != nil {
			out.FinishedAt = shared.CloneTime(run.FinishedAt)
		}
	}
	return out, nil
}

// abortWorkspaceRun 写入 aborted 状态后回读最新 run。
func abortWorkspaceRun(ctx context.Context, svc workspace.Service, input workspaceAbortRunInput) (*workspaceRunDTO, error) {
	if err := requireDependency(svc, "workspace service"); err != nil {
		return nil, err
	}
	runKey, err := requireTrimmed(input.RunKey, "run_key")
	if err != nil {
		return nil, err
	}
	if err := svc.AbortRun(ctx, runKey, strings.TrimSpace(input.UpdatedBy), strings.TrimSpace(input.Reason)); err != nil {
		return nil, err
	}
	run, err := svc.GetRun(ctx, runKey)
	if err != nil {
		return nil, err
	}
	return workspaceRunDTOFromRun(ctx, svc, run)
}

// normalizeWorkspaceListLimit 限制 workspace 列表返回规模。
func normalizeWorkspaceListLimit(limit int) int {
	return normalizeListLimit(limit, defaultWorkspaceListLimit, maxWorkspaceListLimit)
}

// trimNonEmpty 清理文件列表中的空路径。
// 具体路径合法性由 workspace.Service 再校验，避免工具层复制安全规则。
func trimNonEmpty(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		if candidate := strings.TrimSpace(value); candidate != "" {
			trimmed = append(trimmed, candidate)
		}
	}
	if len(trimmed) == 0 {
		return nil
	}
	return trimmed
}

// workspaceCreateScope 从可信工具作用域解析 create 请求允许访问的根目录。
// sourceRoot 为绝对路径时必须落在 roots 内；相对路径使用主 root 作为 CWD。
func workspaceCreateScope(ctx context.Context, sourceRoot string) (string, []string, error) {
	cwd, err := mcpcommon.WorkspaceRootForPathFromContextStrict(ctx, sourceRoot)
	if err != nil {
		return "", nil, err
	}
	roots, err := trustedWorkspaceRoots(ctx)
	if err != nil {
		return "", nil, err
	}
	return cwd, roots, nil
}

// trustedWorkspaceRoots 读取调用方可信 workspace roots，缺失时阻断写工具。
func trustedWorkspaceRoots(ctx context.Context) ([]string, error) {
	roots, err := mcpcommon.WorkspaceRootsFromContextStrict(ctx)
	if err != nil {
		return nil, err
	}
	return roots, nil
}

// marshalMapToJSON 将工具层 metadata map 编成服务层 raw JSON。
func marshalMapToJSON(m map[string]any) (json.RawMessage, error) {
	return marshalRawJSON(m, rawJSONOptions{EmptyObject: true})
}
