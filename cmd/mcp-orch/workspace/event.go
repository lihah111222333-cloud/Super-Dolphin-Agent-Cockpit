package workspace

import (
	"encoding/json"
	"time"
)

// Event type constants for workspace events.
// These are self-contained within the workspace module—no dependency on
// internal/dto/shared.
const (
	EventTypeWorkspaceRunCreated       uint32 = 1400
	EventTypeWorkspaceRunStatusChanged uint32 = 1401
	EventTypeWorkspaceRunMerged        uint32 = 1402
	EventTypeWorkspaceRunAborted       uint32 = 1403
	EventTypeWorkspaceRunMergeError    uint32 = 1404
)

// EventHeader is a minimal event header for workspace events.
type EventHeader struct {
	Timestamp time.Time `json:"timestamp"`
}

// WorkspaceRunHeader identifies a workspace run event.
type WorkspaceRunHeader struct {
	EventHeader
	DagKey string `json:"dag_key,omitempty"`
	RunKey string `json:"run_key"`
}

// WorkspaceRunCreated reports a new workspace run.
type WorkspaceRunCreated struct {
	WorkspaceRunHeader
	ID            int64           `json:"id,omitempty"`
	SourceRoot    string          `json:"source_root"`
	WorkspacePath string          `json:"workspace_path"`
	Status        string          `json:"status,omitempty"`
	CreatedBy     string          `json:"created_by,omitempty"`
	UpdatedBy     string          `json:"updated_by,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	CreatedAt     time.Time       `json:"created_at,omitempty"`
	UpdatedAt     time.Time       `json:"updated_at,omitempty"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
}

// WorkspaceRunStatusChanged reports a workspace run status transition.
type WorkspaceRunStatusChanged struct {
	WorkspaceRunHeader
	OldStatus string `json:"old_status,omitempty"`
	NewStatus string `json:"new_status"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// WorkspaceRunMerged reports a workspace run merging back to source.
type WorkspaceRunMerged struct {
	WorkspaceRunHeader
	SourceRoot      string `json:"source_root"`
	WorkspacePath   string `json:"workspace_path"`
	Status          string `json:"status,omitempty"`
	DryRun          bool   `json:"dry_run,omitempty"`
	MergedFileCount int    `json:"merged_file_count,omitempty"`
	Removed         int    `json:"removed,omitempty"`
	Conflicts       int    `json:"conflicts,omitempty"`
	Unchanged       int    `json:"unchanged,omitempty"`
	Errors          int    `json:"errors,omitempty"`
	UpdatedBy       string `json:"updated_by,omitempty"`
}

// WorkspaceRunAborted reports a workspace run abort request.
type WorkspaceRunAborted struct {
	WorkspaceRunHeader
	SourceRoot    string `json:"source_root,omitempty"`
	WorkspacePath string `json:"workspace_path,omitempty"`
	Status        string `json:"status,omitempty"`
	Reason        string `json:"reason,omitempty"`
	UpdatedBy     string `json:"updated_by,omitempty"`
}

// WorkspaceRunMergeError reports a merge attempt that could not complete cleanly.
type WorkspaceRunMergeError struct {
	WorkspaceRunHeader
	SourceRoot    string `json:"source_root"`
	WorkspacePath string `json:"workspace_path"`
	Conflicts     int    `json:"conflicts,omitempty"`
	Errors        int    `json:"errors,omitempty"`
	Message       string `json:"message,omitempty"`
	UpdatedBy     string `json:"updated_by,omitempty"`
}

func (WorkspaceRunCreated) Type() uint32       { return EventTypeWorkspaceRunCreated }
func (WorkspaceRunStatusChanged) Type() uint32 { return EventTypeWorkspaceRunStatusChanged }
func (WorkspaceRunMerged) Type() uint32        { return EventTypeWorkspaceRunMerged }
func (WorkspaceRunAborted) Type() uint32       { return EventTypeWorkspaceRunAborted }
func (WorkspaceRunMergeError) Type() uint32    { return EventTypeWorkspaceRunMergeError }
