package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	workspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/workspace"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
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
}

type workspaceListRunsInput struct {
	Status string `json:"status,omitempty"`
	DagKey string `json:"dag_key,omitempty"`
	Limit  int    `json:"limit,omitempty"`
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

func HandleWorkspaceCreateRun(svc workspace.Service) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in WorkspaceCreateRunRequest
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		return createWorkspaceRun(ctx, svc, in)
	}
}

func HandleWorkspaceGetRun(svc workspace.Service) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in workspaceGetRunInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		return getWorkspaceRun(ctx, svc, in)
	}
}

func HandleWorkspaceListRuns(svc workspace.Service) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in workspaceListRunsInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		return listWorkspaceRuns(ctx, svc, in)
	}
}

func HandleWorkspaceMergeRun(svc workspace.Service) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in WorkspaceMergeRunRequest
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		return mergeWorkspaceRun(ctx, svc, in)
	}
}

func HandleWorkspaceAbortRun(svc workspace.Service) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in workspaceAbortRunInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		return abortWorkspaceRun(ctx, svc, in)
	}
}

func workspaceToolDefinitions(svc workspace.Service) []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "workspace_create_run",
			Description: "Create a virtual workspace run. Filesystem workspace is used for edits; run status and file states are persisted in PostgreSQL.",
			InputSchema: ObjectSchema(map[string]Schema{
				"run_key":     StringSchema("Optional run key. Auto-generated if omitted."),
				"dag_key":     StringSchema("Related DAG key (optional)."),
				"source_root": StringSchema("Absolute or relative source project root."),
				"created_by":  StringSchema("Creator identifier (optional)."),
				"files":       ArraySchema(StringSchema("Relative file path to copy into the workspace."), "Optional bootstrap files to copy from source root to workspace."),
				"metadata":    RawObjectSchema("Optional metadata for the run record."),
			}, "source_root"),
			Handler: HandleWorkspaceCreateRun(svc),
		},
		{
			Name:        "workspace_get_run",
			Description: "Get workspace run detail by run key.",
			InputSchema: ObjectSchema(map[string]Schema{
				"run_key": StringSchema("Workspace run key."),
			}, "run_key"),
			Handler: HandleWorkspaceGetRun(svc),
		},
		{
			Name:        "workspace_list_runs",
			Description: "List workspace runs with optional status and DAG filters.",
			InputSchema: ObjectSchema(map[string]Schema{
				"status":  StringSchema("Optional run status filter."),
				"dag_key": StringSchema("Optional DAG key filter."),
				"limit":   IntegerSchema("Max number of runs to return."),
			}),
			Handler: HandleWorkspaceListRuns(svc),
		},
		{
			Name:        "workspace_merge_run",
			Description: "Merge changed files from virtual workspace back to source root with conflict detection. Also updates PostgreSQL run and file states.",
			InputSchema: ObjectSchema(map[string]Schema{
				"run_key":        StringSchema("Workspace run key."),
				"updated_by":     StringSchema("Operator identifier (optional)."),
				"dry_run":        BooleanSchema("Only simulate merge without writing source files."),
				"delete_removed": BooleanSchema("Delete source files removed in workspace when safe."),
			}, "run_key"),
			Handler: HandleWorkspaceMergeRun(svc),
		},
		{
			Name:        "workspace_abort_run",
			Description: "Abort a workspace run and mark it as aborted in PostgreSQL state.",
			InputSchema: ObjectSchema(map[string]Schema{
				"run_key":    StringSchema("Workspace run key."),
				"updated_by": StringSchema("Operator identifier (optional)."),
				"reason":     StringSchema("Abort reason (optional)."),
			}, "run_key"),
			Handler: HandleWorkspaceAbortRun(svc),
		},
	}
}

// V3 validates source_root and trims optional fields at the handler boundary.
// V2 relied on downstream workspace services to enforce the same constraints.
func createWorkspaceRun(ctx context.Context, svc workspace.Service, input WorkspaceCreateRunRequest) (*workspaceRunDTO, error) {
	if svc == nil {
		return nil, errors.New("workspace service is not configured")
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
	if svc == nil {
		return nil, errors.New("workspace service is not configured")
	}
	runKey, err := requireTrimmed(input.RunKey, "run_key")
	if err != nil {
		return nil, err
	}
	run, err := svc.GetRun(ctx, runKey)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return nil, fmt.Errorf("workspace run %s not found", runKey)
		}
		return nil, err
	}
	if run == nil {
		return nil, fmt.Errorf("workspace run %s not found", runKey)
	}
	return workspaceRunDTOFromRun(ctx, svc, run)
}

func listWorkspaceRuns(ctx context.Context, svc workspace.Service, input workspaceListRunsInput) ([]workspaceRunDTO, error) {
	if svc == nil {
		return nil, errors.New("workspace service is not configured")
	}
	runs, err := svc.ListRuns(ctx, strings.TrimSpace(input.Status), strings.TrimSpace(input.DagKey), normalizeWorkspaceListLimit(input.Limit))
	if err != nil {
		return nil, err
	}
	return mapWorkspaceRuns(ctx, svc, runs)
}

func mergeWorkspaceRun(ctx context.Context, svc workspace.Service, input WorkspaceMergeRunRequest) (*WorkspaceMergeRunResult, error) {
	if svc == nil {
		return nil, errors.New("workspace service is not configured")
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
			out.FinishedAt = cloneTime(run.FinishedAt)
		}
	}
	return out, nil
}

func abortWorkspaceRun(ctx context.Context, svc workspace.Service, input workspaceAbortRunInput) (*workspaceRunDTO, error) {
	if svc == nil {
		return nil, errors.New("workspace service is not configured")
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
	if limit <= 0 || limit > maxWorkspaceListLimit {
		return defaultWorkspaceListLimit
	}
	return limit
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
	if len(m) == 0 {
		return nil, nil
	}
	return json.Marshal(m)
}
