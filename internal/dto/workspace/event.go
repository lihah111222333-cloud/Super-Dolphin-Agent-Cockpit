package workspace

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

// WorkspaceRunCreated reports a new workspace run.
type WorkspaceRunCreated struct {
	shared.WorkspaceRunHeader
	SourceRoot    string `json:"sourceRoot"`
	WorkspacePath string `json:"workspacePath"`
	CreatedBy     string `json:"createdBy,omitempty"`
}

// WorkspaceRunStatusChanged reports a workspace run status transition.
type WorkspaceRunStatusChanged struct {
	shared.WorkspaceRunHeader
	OldStatus string `json:"oldStatus,omitempty"`
	NewStatus string `json:"newStatus"`
	UpdatedBy string `json:"updatedBy,omitempty"`
}

// WorkspaceRunMerged reports a workspace run merging back to source.
type WorkspaceRunMerged struct {
	shared.WorkspaceRunHeader
	SourceRoot      string `json:"sourceRoot"`
	WorkspacePath   string `json:"workspacePath"`
	MergedFileCount int    `json:"mergedFileCount,omitempty"`
	UpdatedBy       string `json:"updatedBy,omitempty"`
}

// WorkspaceRunAborted reports a workspace run abort request.
type WorkspaceRunAborted struct {
	shared.WorkspaceRunHeader
	Reason    string `json:"reason,omitempty"`
	UpdatedBy string `json:"updatedBy,omitempty"`
}

// WorkspaceRunMergeError reports a merge attempt that could not complete cleanly.
type WorkspaceRunMergeError struct {
	shared.WorkspaceRunHeader
	SourceRoot    string `json:"sourceRoot"`
	WorkspacePath string `json:"workspacePath"`
	Conflicts     int    `json:"conflicts,omitempty"`
	Errors        int    `json:"errors,omitempty"`
	Message       string `json:"message,omitempty"`
	UpdatedBy     string `json:"updatedBy,omitempty"`
}

func (WorkspaceRunCreated) Type() uint32       { return shared.EventTypeWorkspaceRunCreated }
func (WorkspaceRunStatusChanged) Type() uint32 { return shared.EventTypeWorkspaceRunStatusChanged }
func (WorkspaceRunMerged) Type() uint32        { return shared.EventTypeWorkspaceRunMerged }
func (WorkspaceRunAborted) Type() uint32       { return shared.EventTypeWorkspaceRunAborted }
func (WorkspaceRunMergeError) Type() uint32    { return shared.EventTypeWorkspaceRunMergeError }
