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

func (WorkspaceRunCreated) Type() uint32       { return shared.EventTypeWorkspaceRunCreated }
func (WorkspaceRunStatusChanged) Type() uint32 { return shared.EventTypeWorkspaceRunStatusChanged }
func (WorkspaceRunMerged) Type() uint32        { return shared.EventTypeWorkspaceRunMerged }
