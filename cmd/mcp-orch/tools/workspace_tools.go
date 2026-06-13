package tools

import (
	"context"
	"encoding/json"
	"strings"

	workspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/workspace"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	defaultWorkspaceListLimit = 200
	maxWorkspaceListLimit     = 5000
)

// Workspace tool schemas inherit ObjectSchema's additionalProperties=false.
// V3 adds handler-level input validation, while V2 relied on downstream
// workspace services for the same validation.
// Path sandboxing remains the responsibility of the injected workspace.Service.

type WorkspaceCreateRunRequest struct {
	RunKey     string         `json:"run_key,omitempty"`
	DagKey     string         `json:"dag_key,omitempty"`
	SourceRoot string         `json:"source_root"`
	CreatedBy  string         `json:"created_by,omitempty"`
	Files      []string       `json:"files,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type workspaceGetRunInput struct {
	RunKey string `json:"run_key"`
	Pos    string `json:"pos,omitempty"`
}

type workspaceListRunsInput struct {
	Status   string `json:"status,omitempty"`
	DagKey   string `json:"dag_key,omitempty"`
	Pos      string `json:"pos,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Envelope bool   `json:"envelope,omitempty"`
}

type WorkspaceListRunsOutput struct {
	Runs      []workspaceRunDTO `json:"runs"`
	Data      []workspaceRunDTO `json:"data"`
	Total     int               `json:"total"`
	Showing   int               `json:"showing"`
	Truncated bool              `json:"truncated"`
	Hint      string            `json:"hint,omitempty"`
}

type WorkspaceMergeRunRequest struct {
	RunKey        string `json:"run_key"`
	UpdatedBy     string `json:"updated_by,omitempty"`
	DryRun        bool   `json:"dry_run,omitempty"`
	DeleteRemoved bool   `json:"delete_removed,omitempty"`
}

type workspaceAbortRunInput struct {
	RunKey    string `json:"run_key"`
	UpdatedBy string `json:"updated_by,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// HandleWorkspaceCreateRun 处理工作区create运行记录。
func HandleWorkspaceCreateRun(svc workspace.Service) ToolHandler {
	return makeHandler(svc, "workspace service", func(ctx context.Context, in WorkspaceCreateRunRequest) (*workspaceRunDTO, error) {
		return createWorkspaceRun(ctx, svc, in)
	})
}

// HandleWorkspaceGetRun 处理工作区get运行记录。
func HandleWorkspaceGetRun(svc workspace.Service) ToolHandler {
	return makeHandler(svc, "workspace service", func(ctx context.Context, in workspaceGetRunInput) (*workspaceRunDTO, error) {
		return getWorkspaceRun(ctx, svc, in)
	})
}

// HandleWorkspaceListRuns 处理工作区list运行记录。
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

// HandleWorkspaceMergeRun 处理工作区merge运行记录。
func HandleWorkspaceMergeRun(svc workspace.Service) ToolHandler {
	return makeHandler(svc, "workspace service", func(ctx context.Context, in WorkspaceMergeRunRequest) (*WorkspaceMergeRunResult, error) {
		return mergeWorkspaceRun(ctx, svc, in)
	})
}

// HandleWorkspaceAbortRun 处理工作区abort运行记录。
func HandleWorkspaceAbortRun(svc workspace.Service) ToolHandler {
	return makeHandler(svc, "workspace service", func(ctx context.Context, in workspaceAbortRunInput) (*workspaceRunDTO, error) {
		return abortWorkspaceRun(ctx, svc, in)
	})
}

// workspaceToolDefinitions 处理工作区工具definitions。
func workspaceToolDefinitions(svc workspace.Service) []ToolDefinition {
	return buildToolDefinitions(
		defineTool("workspace_create_run", "Create a virtual workspace run. Filesystem workspace is used for edits; run status and file states are persisted in PostgreSQL.", ObjectSchema(map[string]Schema{
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
		defineTool("workspace_merge_run", "Merge changed files from virtual workspace back to source root with conflict detection. Also updates PostgreSQL run and file states.", ObjectSchema(map[string]Schema{
			"run_key":        StringSchema("Workspace run key."),
			"updated_by":     StringSchema("Operator identifier (optional)."),
			"dry_run":        BooleanSchema("Only simulate merge without writing source files."),
			"delete_removed": BooleanSchema("Delete source files removed in workspace when safe."),
		}, "run_key"), HandleWorkspaceMergeRun(svc)),
		defineTool("workspace_abort_run", "Abort a workspace run and mark it as aborted in PostgreSQL state.", ObjectSchema(map[string]Schema{
			"run_key":    StringSchema("Workspace run key."),
			"updated_by": StringSchema("Operator identifier (optional)."),
			"reason":     StringSchema("Abort reason (optional)."),
		}, "run_key"), HandleWorkspaceAbortRun(svc)),
	)
}

// V3 validates source_root and trims optional fields at the handler boundary.
// V2 relied on downstream workspace services to enforce the same constraints.
func createWorkspaceRun(ctx context.Context, svc workspace.Service, input WorkspaceCreateRunRequest) (*workspaceRunDTO, error) {
	if err := requireDependency(svc, "workspace service"); err != nil {
		return nil, err
	}
	sourceRoot, err := requireTrimmed(input.SourceRoot, "source_root")
	if err != nil {
		return nil, err
	}
	metadata, err := marshalMapToJSON(input.Metadata)
	if err != nil {
		return nil, err
	}
	run, err := svc.CreateRun(ctx, workspace.CreateRunRequest{
		RunKey:     strings.TrimSpace(input.RunKey),
		DagKey:     strings.TrimSpace(input.DagKey),
		SourceRoot: sourceRoot,
		CreatedBy:  strings.TrimSpace(input.CreatedBy),
		Files:      trimNonEmpty(input.Files),
		Metadata:   metadata,
	})
	if err != nil {
		return nil, err
	}
	return workspaceRunDTOFromRun(ctx, svc, run)
}

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

// mergeWorkspaceRun 合并工作区运行记录。
func mergeWorkspaceRun(ctx context.Context, svc workspace.Service, input WorkspaceMergeRunRequest) (*WorkspaceMergeRunResult, error) {
	if err := requireDependency(svc, "workspace service"); err != nil {
		return nil, err
	}
	runKey, err := requireTrimmed(input.RunKey, "run_key")
	if err != nil {
		return nil, err
	}
	result, err := svc.MergeRun(ctx, workspace.MergeRunRequest{
		RunKey:        runKey,
		UpdatedBy:     strings.TrimSpace(input.UpdatedBy),
		DryRun:        input.DryRun,
		DeleteRemoved: input.DeleteRemoved,
	})
	if err != nil {
		return nil, err
	}
	out := convertMergeResult(result, input.DeleteRemoved)
	// MergeRunResult from workspace.Service lacks FinishedAt; read it
	// from the persisted run when the merge was not a dry run.
	if !input.DryRun {
		if run, runErr := svc.GetRun(ctx, runKey); runErr == nil && run != nil {
			out.FinishedAt = shared.CloneTime(run.FinishedAt)
		}
	}
	return out, nil
}

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

func normalizeWorkspaceListLimit(limit int) int {
	return normalizeListLimit(limit, defaultWorkspaceListLimit, maxWorkspaceListLimit)
}

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

func marshalMapToJSON(m map[string]any) (json.RawMessage, error) {
	return marshalRawJSON(m, rawJSONOptions{EmptyObject: true})
}
