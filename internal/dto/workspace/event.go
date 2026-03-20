package workspace

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

// WorkspaceRunCreated reports a new workspace run.
type WorkspaceRunCreated struct {
	shared.WorkspaceRunHeader
	SourceRoot    string `json:"source_root"`
	WorkspacePath string `json:"workspace_path"`
	CreatedBy     string `json:"created_by,omitempty"`
}

// WorkspaceRunStatusChanged reports a workspace run status transition.
type WorkspaceRunStatusChanged struct {
	shared.WorkspaceRunHeader
	OldStatus string `json:"old_status,omitempty"`
	NewStatus string `json:"new_status"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// WorkspaceRunMerged reports a workspace run merging back to source.
type WorkspaceRunMerged struct {
	shared.WorkspaceRunHeader
	SourceRoot      string `json:"source_root"`
	WorkspacePath   string `json:"workspace_path"`
	MergedFileCount int    `json:"merged_file_count,omitempty"`
	UpdatedBy       string `json:"updated_by,omitempty"`
}

// WorkspaceRunAborted reports a workspace run abort request.
type WorkspaceRunAborted struct {
	shared.WorkspaceRunHeader
	Reason    string `json:"reason,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// WorkspaceRunMergeError reports a merge attempt that could not complete cleanly.
type WorkspaceRunMergeError struct {
	shared.WorkspaceRunHeader
	SourceRoot    string `json:"source_root"`
	WorkspacePath string `json:"workspace_path"`
	Conflicts     int    `json:"conflicts,omitempty"`
	Errors        int    `json:"errors,omitempty"`
	Message       string `json:"message,omitempty"`
	UpdatedBy     string `json:"updated_by,omitempty"`
}

func (WorkspaceRunCreated) Type() uint32       { return shared.EventTypeWorkspaceRunCreated }
func (WorkspaceRunStatusChanged) Type() uint32 { return shared.EventTypeWorkspaceRunStatusChanged }
func (WorkspaceRunMerged) Type() uint32        { return shared.EventTypeWorkspaceRunMerged }
func (WorkspaceRunAborted) Type() uint32       { return shared.EventTypeWorkspaceRunAborted }
func (WorkspaceRunMergeError) Type() uint32    { return shared.EventTypeWorkspaceRunMergeError }
