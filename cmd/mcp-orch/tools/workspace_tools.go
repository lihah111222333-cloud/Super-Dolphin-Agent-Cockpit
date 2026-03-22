package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	workspacestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/workspace"
)

const (
	defaultWorkspaceListLimit = 200
	maxWorkspaceListLimit     = 5000
)

type WorkspaceStore interface {
	workspacestore.Store
	CreateRun(ctx context.Context, req WorkspaceCreateRunRequest) (*workspacestore.WorkspaceRun, error)
	MergeRun(ctx context.Context, req WorkspaceMergeRunRequest) (*WorkspaceMergeRunResult, error)
	AbortRun(ctx context.Context, runKey, updatedBy, reason string) (*workspacestore.WorkspaceRun, error)
}

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
	DryRun        bool                       `json:"dry_run"`
	Merged        int                        `json:"merged"`
	Removed       int                        `json:"removed"`
	Conflicts     int                        `json:"conflicts"`
	Unchanged     int                        `json:"unchanged"`
	Errors        int                        `json:"errors"`
	Files         []WorkspaceMergeFileResult `json:"files,omitempty"`
}

func HandleWorkspaceCreateRun(store WorkspaceStore) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in WorkspaceCreateRunRequest
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		return createWorkspaceRun(ctx, store, in)
	}
}

func HandleWorkspaceGetRun(store WorkspaceStore) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in workspaceGetRunInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		return getWorkspaceRun(ctx, store, in)
	}
}

func HandleWorkspaceListRuns(store WorkspaceStore) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in workspaceListRunsInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		return listWorkspaceRuns(ctx, store, in)
	}
}

func HandleWorkspaceMergeRun(store WorkspaceStore) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in WorkspaceMergeRunRequest
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		return mergeWorkspaceRun(ctx, store, in)
	}
}

func HandleWorkspaceAbortRun(store WorkspaceStore) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in workspaceAbortRunInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		return abortWorkspaceRun(ctx, store, in)
	}
}

func workspaceToolDefinitions(store WorkspaceStore) []ToolDefinition {
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
			Handler: HandleWorkspaceCreateRun(store),
		},
		{
			Name:        "workspace_get_run",
			Description: "Get workspace run detail by run key.",
			InputSchema: ObjectSchema(map[string]Schema{
				"run_key": StringSchema("Workspace run key."),
			}, "run_key"),
			Handler: HandleWorkspaceGetRun(store),
		},
		{
			Name:        "workspace_list_runs",
			Description: "List workspace runs with optional status and DAG filters.",
			InputSchema: ObjectSchema(map[string]Schema{
				"status":  StringSchema("Optional run status filter."),
				"dag_key": StringSchema("Optional DAG key filter."),
				"limit":   IntegerSchema("Max number of runs to return."),
			}),
			Handler: HandleWorkspaceListRuns(store),
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
			Handler: HandleWorkspaceMergeRun(store),
		},
		{
			Name:        "workspace_abort_run",
			Description: "Abort a workspace run and mark it as aborted in PostgreSQL state.",
			InputSchema: ObjectSchema(map[string]Schema{
				"run_key":    StringSchema("Workspace run key."),
				"updated_by": StringSchema("Operator identifier (optional)."),
				"reason":     StringSchema("Abort reason (optional)."),
			}, "run_key"),
			Handler: HandleWorkspaceAbortRun(store),
		},
	}
}

func createWorkspaceRun(ctx context.Context, store WorkspaceStore, input WorkspaceCreateRunRequest) (*workspacestore.WorkspaceRun, error) {
	if store == nil {
		return nil, errors.New("workspace store is not configured")
	}
	sourceRoot, err := requireTrimmed(input.SourceRoot, "source_root")
	if err != nil {
		return nil, err
	}
	req := input
	req.SourceRoot = sourceRoot
	req.RunKey = strings.TrimSpace(req.RunKey)
	req.DagKey = strings.TrimSpace(req.DagKey)
	req.CreatedBy = strings.TrimSpace(req.CreatedBy)
	req.Files = trimNonEmpty(req.Files)
	return store.CreateRun(ctx, req)
}

func getWorkspaceRun(ctx context.Context, store WorkspaceStore, input workspaceGetRunInput) (*workspacestore.WorkspaceRun, error) {
	if store == nil {
		return nil, errors.New("workspace store is not configured")
	}
	runKey, err := requireTrimmed(input.RunKey, "run_key")
	if err != nil {
		return nil, err
	}
	return store.GetRun(ctx, runKey)
}

func listWorkspaceRuns(ctx context.Context, store WorkspaceStore, input workspaceListRunsInput) ([]workspacestore.WorkspaceRun, error) {
	if store == nil {
		return nil, errors.New("workspace store is not configured")
	}
	return store.ListRuns(ctx, workspacestore.ListRunsFilter{
		Status: strings.TrimSpace(input.Status),
		DagKey: strings.TrimSpace(input.DagKey),
		Limit:  normalizeWorkspaceListLimit(input.Limit),
	})
}

func mergeWorkspaceRun(ctx context.Context, store WorkspaceStore, input WorkspaceMergeRunRequest) (*WorkspaceMergeRunResult, error) {
	if store == nil {
		return nil, errors.New("workspace store is not configured")
	}
	runKey, err := requireTrimmed(input.RunKey, "run_key")
	if err != nil {
		return nil, err
	}
	req := input
	req.RunKey = runKey
	req.UpdatedBy = strings.TrimSpace(req.UpdatedBy)
	return store.MergeRun(ctx, req)
}

func abortWorkspaceRun(ctx context.Context, store WorkspaceStore, input workspaceAbortRunInput) (*workspacestore.WorkspaceRun, error) {
	if store == nil {
		return nil, errors.New("workspace store is not configured")
	}
	runKey, err := requireTrimmed(input.RunKey, "run_key")
	if err != nil {
		return nil, err
	}
	return store.AbortRun(ctx, runKey, strings.TrimSpace(input.UpdatedBy), strings.TrimSpace(input.Reason))
}

func normalizeWorkspaceListLimit(limit int) int32 {
	if limit <= 0 || limit > maxWorkspaceListLimit {
		return defaultWorkspaceListLimit
	}
	return int32(limit)
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
