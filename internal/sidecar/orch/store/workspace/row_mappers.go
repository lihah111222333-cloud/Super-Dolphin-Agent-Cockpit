package workspace

import "github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/sqlc"

func fromSQLCUpsertRun(row sqlc.UpsertWorkspaceRunRow) WorkspaceRun {
	return fromSQLCRunRaw(row.ID, row.RunKey, row.DagKey, row.SourceRoot, row.WorkspacePath, row.Status, row.CreatedBy, row.UpdatedBy, row.Metadata, row.CreatedAt, row.UpdatedAt, row.FinishedAt)
}

func fromSQLCGetRun(row sqlc.GetWorkspaceRunRow) WorkspaceRun {
	return fromSQLCRunRaw(row.ID, row.RunKey, row.DagKey, row.SourceRoot, row.WorkspacePath, row.Status, row.CreatedBy, row.UpdatedBy, row.Metadata, row.CreatedAt, row.UpdatedAt, row.FinishedAt)
}

func fromSQLCUpdateRunStatus(row sqlc.UpdateWorkspaceRunStatusRow) WorkspaceRun {
	return fromSQLCRunRaw(row.ID, row.RunKey, row.DagKey, row.SourceRoot, row.WorkspacePath, row.Status, row.CreatedBy, row.UpdatedBy, row.Metadata, row.CreatedAt, row.UpdatedAt, row.FinishedAt)
}

func fromSQLCTransitionRunStatus(row sqlc.TransitionWorkspaceRunStatusRow) WorkspaceRun {
	return fromSQLCRunRaw(row.ID, row.RunKey, row.DagKey, row.SourceRoot, row.WorkspacePath, row.Status, row.CreatedBy, row.UpdatedBy, row.Metadata, row.CreatedAt, row.UpdatedAt, row.FinishedAt)
}

func fromSQLCRunRaw(id int64, runKey, dagKey, sourceRoot, workspacePath, status, createdBy, updatedBy string, metadata []byte, createdAt, updatedAt int64, finishedAt *int64) WorkspaceRun {
	return WorkspaceRun{
		ID:            id,
		RunKey:        runKey,
		DagKey:        dagKey,
		SourceRoot:    sourceRoot,
		WorkspacePath: workspacePath,
		Status:        status,
		CreatedBy:     createdBy,
		UpdatedBy:     updatedBy,
		Metadata:      metadata,
		CreatedAt:     sqlc.TimeValue(createdAt),
		UpdatedAt:     sqlc.TimeValue(updatedAt),
		FinishedAt:    sqlc.TimePtr(finishedAt),
	}
}
