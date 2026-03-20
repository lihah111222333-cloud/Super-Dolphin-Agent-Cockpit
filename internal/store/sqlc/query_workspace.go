package sqlc

import "context"

const (
	upsertWorkspaceRunSQL           = `INSERT INTO workspace_runs ( run_key, dag_key, source_root, workspace_path, status, created_by, updated_by, metadata, updated_at, finished_at ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, NOW(), $9) ON CONFLICT (run_key) DO UPDATE SET dag_key = EXCLUDED.dag_key, source_root = EXCLUDED.source_root, workspace_path = EXCLUDED.workspace_path, status = EXCLUDED.status, updated_by = EXCLUDED.updated_by, metadata = EXCLUDED.metadata, updated_at = NOW(), finished_at = EXCLUDED.finished_at RETURNING id, run_key, dag_key, source_root, workspace_path, status, created_by, updated_by, metadata, created_at, updated_at, finished_at;`
	getWorkspaceRunSQL              = `SELECT id, run_key, dag_key, source_root, workspace_path, status, created_by, updated_by, metadata, created_at, updated_at, finished_at FROM workspace_runs WHERE run_key = $1;`
	listWorkspaceRunsSQL            = `SELECT id, run_key, dag_key, source_root, workspace_path, status, created_by, updated_by, metadata, created_at, updated_at, finished_at FROM workspace_runs WHERE ($1::text = '' OR status = $1) AND ($2::text = '' OR dag_key = $2) ORDER BY updated_at DESC, id DESC LIMIT $3;`
	updateWorkspaceRunStatusSQL     = `UPDATE workspace_runs SET status = $1, updated_by = $2, metadata = $3::jsonb, updated_at = NOW(), finished_at = CASE WHEN $1 IN ('merged', 'aborted', 'failed') THEN NOW() WHEN $1 = 'active' THEN NULL ELSE finished_at END WHERE run_key = $4 RETURNING id, run_key, dag_key, source_root, workspace_path, status, created_by, updated_by, metadata, created_at, updated_at, finished_at;`
	transitionWorkspaceRunStatusSQL = `UPDATE workspace_runs SET status = $1, updated_by = $2, metadata = $3::jsonb, updated_at = NOW(), finished_at = CASE WHEN $1 IN ('merged', 'aborted', 'failed') THEN NOW() WHEN $1 = 'active' THEN NULL ELSE finished_at END WHERE run_key = $4 AND status = $5 RETURNING id, run_key, dag_key, source_root, workspace_path, status, created_by, updated_by, metadata, created_at, updated_at, finished_at;`
	upsertWorkspaceRunFileSQL       = `INSERT INTO workspace_run_files ( run_key, relative_path, baseline_sha256, workspace_sha256, source_sha256_before, source_sha256_after, state, last_error, updated_at ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW()) ON CONFLICT (run_key, relative_path) DO UPDATE SET baseline_sha256 = EXCLUDED.baseline_sha256, workspace_sha256 = EXCLUDED.workspace_sha256, source_sha256_before = EXCLUDED.source_sha256_before, source_sha256_after = EXCLUDED.source_sha256_after, state = EXCLUDED.state, last_error = EXCLUDED.last_error, updated_at = NOW() RETURNING id, run_key, relative_path, baseline_sha256, workspace_sha256, source_sha256_before, source_sha256_after, state, last_error, created_at, updated_at;`
	getWorkspaceRunFileSQL          = `SELECT id, run_key, relative_path, baseline_sha256, workspace_sha256, source_sha256_before, source_sha256_after, state, last_error, created_at, updated_at FROM workspace_run_files WHERE run_key = $1 AND relative_path = $2;`
	listWorkspaceRunFilesSQL        = `SELECT id, run_key, relative_path, baseline_sha256, workspace_sha256, source_sha256_before, source_sha256_after, state, last_error, created_at, updated_at FROM workspace_run_files WHERE ($1::text = '' OR run_key = $1) AND ($2::text = '' OR state = $2) ORDER BY updated_at DESC, id DESC LIMIT $3;`
)

func scanWorkspaceRun(row rowScanner) (WorkspaceRun, error) {
	var item WorkspaceRun
	err := row.Scan(&item.ID, &item.RunKey, &item.DagKey, &item.SourceRoot, &item.WorkspacePath, &item.Status, &item.CreatedBy, &item.UpdatedBy, &item.Metadata, &item.CreatedAt, &item.UpdatedAt, &item.FinishedAt)
	return item, err
}

func scanWorkspaceRunFile(row rowScanner) (WorkspaceRunFile, error) {
	var item WorkspaceRunFile
	err := row.Scan(&item.ID, &item.RunKey, &item.RelativePath, &item.BaselineSHA256, &item.WorkspaceSHA256, &item.SourceSHA256Before, &item.SourceSHA256After, &item.State, &item.LastError, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func (q *Queries) UpsertWorkspaceRun(ctx context.Context, arg UpsertWorkspaceRunParams) (WorkspaceRun, error) {
	return queryOne(ctx, q, upsertWorkspaceRunSQL, scanWorkspaceRun, arg.RunKey, arg.DagKey, arg.SourceRoot, arg.WorkspacePath, arg.Status, arg.CreatedBy, arg.UpdatedBy, arg.Metadata, arg.FinishedAt)
}

func (q *Queries) GetWorkspaceRun(ctx context.Context, runKey string) (WorkspaceRun, error) {
	return queryOne(ctx, q, getWorkspaceRunSQL, scanWorkspaceRun, runKey)
}

func (q *Queries) ListWorkspaceRuns(ctx context.Context, arg ListWorkspaceRunsParams) ([]WorkspaceRun, error) {
	return queryMany(ctx, q, listWorkspaceRunsSQL, scanWorkspaceRun, arg.Status, arg.DagKey, arg.Limit)
}

func (q *Queries) UpdateWorkspaceRunStatus(ctx context.Context, arg UpdateWorkspaceRunStatusParams) (WorkspaceRun, error) {
	return queryOne(ctx, q, updateWorkspaceRunStatusSQL, scanWorkspaceRun, arg.Status, arg.UpdatedBy, arg.Metadata, arg.RunKey)
}

func (q *Queries) TransitionWorkspaceRunStatus(ctx context.Context, arg TransitionWorkspaceRunStatusParams) (WorkspaceRun, error) {
	return queryOne(ctx, q, transitionWorkspaceRunStatusSQL, scanWorkspaceRun, arg.Status, arg.UpdatedBy, arg.Metadata, arg.RunKey, arg.FromStatus)
}

func (q *Queries) UpsertWorkspaceRunFile(ctx context.Context, arg UpsertWorkspaceRunFileParams) (WorkspaceRunFile, error) {
	return queryOne(ctx, q, upsertWorkspaceRunFileSQL, scanWorkspaceRunFile, arg.RunKey, arg.RelativePath, arg.BaselineSHA256, arg.WorkspaceSHA256, arg.SourceSHA256Before, arg.SourceSHA256After, arg.State, arg.LastError)
}

func (q *Queries) GetWorkspaceRunFile(ctx context.Context, arg GetWorkspaceRunFileParams) (WorkspaceRunFile, error) {
	return queryOne(ctx, q, getWorkspaceRunFileSQL, scanWorkspaceRunFile, arg.RunKey, arg.RelativePath)
}

func (q *Queries) ListWorkspaceRunFiles(ctx context.Context, arg ListWorkspaceRunFilesParams) ([]WorkspaceRunFile, error) {
	return queryMany(ctx, q, listWorkspaceRunFilesSQL, scanWorkspaceRunFile, arg.RunKey, arg.State, arg.Limit)
}
