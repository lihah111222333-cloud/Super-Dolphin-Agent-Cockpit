package workspace

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlctx"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

type store struct {
	db sqlc.DBTX
	q  *sqlc.Queries
}

func NewStore(db sqlc.DBTX) Store { return &store{db: db, q: sqlc.New(db)} }

func (s *store) WithTx(ctx context.Context, fn func(txStore Store) error) error {
	err := sqlctx.WithTx(ctx, s.db, s.q, func(txq *sqlc.Queries, tx sqlc.DBTX) error {
		return fn(&store{db: tx, q: txq})
	})
	return wrapWorkspaceError(err, "with_tx", "workspace")
}

func (s *store) UpsertRun(ctx context.Context, run WorkspaceRun) (*WorkspaceRun, error) {
	row, err := s.q.UpsertWorkspaceRun(ctx, sqlc.UpsertWorkspaceRunParams{
		RunKey:        run.RunKey,
		DagKey:        run.DagKey,
		SourceRoot:    run.SourceRoot,
		WorkspacePath: run.WorkspacePath,
		Status:        run.Status,
		CreatedBy:     run.CreatedBy,
		UpdatedBy:     run.UpdatedBy,
		Metadata:      run.Metadata,
		FinishedAt:    sqlc.TimeValuePtr(run.FinishedAt),
	})
	if err != nil {
		return nil, wrapWorkspaceError(err, "upsert", "workspace_run")
	}
	mapped := fromSQLCRun(row)
	return &mapped, nil
}

func (s *store) GetRun(ctx context.Context, runKey string) (*WorkspaceRun, error) {
	row, err := s.q.GetWorkspaceRun(ctx, sqlc.GetWorkspaceRunParams{RunKey: runKey})
	if err != nil {
		return nil, wrapWorkspaceError(err, "get", "workspace_run")
	}
	mapped := fromSQLCRun(row)
	return &mapped, nil
}

func (s *store) ListRuns(ctx context.Context, filter ListRunsFilter) ([]WorkspaceRun, error) {
	rows, err := s.q.ListWorkspaceRuns(ctx, sqlc.ListWorkspaceRunsParams{
		StatusFilter: filter.Status,
		DagKeyFilter: filter.DagKey,
		LimitCount:   int64(filter.Limit),
	})
	if err != nil {
		return nil, wrapWorkspaceError(err, "list", "workspace_run")
	}
	runs := make([]WorkspaceRun, 0, len(rows))
	for _, row := range rows {
		runs = append(runs, fromSQLCRun(row))
	}
	return runs, nil
}

func (s *store) UpdateRunStatus(ctx context.Context, input UpdateRunStatusInput) (*WorkspaceRun, error) {
	row, err := s.q.UpdateWorkspaceRunStatus(ctx, sqlc.UpdateWorkspaceRunStatusParams{
		NewStatus: input.Status,
		UpdatedBy: input.UpdatedBy,
		Metadata:  input.Metadata,
		RunKey:    input.RunKey,
	})
	if err != nil {
		return nil, wrapWorkspaceError(err, "update_status", "workspace_run")
	}
	mapped := fromSQLCRun(row)
	return &mapped, nil
}

func (s *store) TransitionRunStatus(ctx context.Context, input TransitionRunStatusInput) (*WorkspaceRun, error) {
	row, err := s.q.TransitionWorkspaceRunStatus(ctx, sqlc.TransitionWorkspaceRunStatusParams{
		NewStatus:      input.Status,
		UpdatedBy:      input.UpdatedBy,
		Metadata:       input.Metadata,
		RunKey:         input.RunKey,
		ExpectedStatus: input.FromStatus,
	})
	if err != nil {
		return nil, wrapWorkspaceError(err, "transition_status", "workspace_run")
	}
	mapped := fromSQLCRun(row)
	return &mapped, nil
}

func (s *store) UpsertFile(ctx context.Context, file WorkspaceRunFile) (*WorkspaceRunFile, error) {
	row, err := s.q.UpsertWorkspaceRunFile(ctx, sqlc.UpsertWorkspaceRunFileParams{
		RunKey:             file.RunKey,
		RelativePath:       file.RelativePath,
		BaselineSha256:     file.BaselineSHA256,
		WorkspaceSha256:    file.WorkspaceSHA256,
		SourceSha256Before: file.SourceSHA256Before,
		SourceSha256After:  file.SourceSHA256After,
		State:              file.State,
		LastError:          file.LastError,
	})
	if err != nil {
		return nil, wrapWorkspaceError(err, "upsert", "workspace_run_file")
	}
	mapped := fromSQLCFile(row)
	return &mapped, nil
}

func (s *store) GetFile(ctx context.Context, runKey, relativePath string) (*WorkspaceRunFile, error) {
	row, err := s.q.GetWorkspaceRunFile(ctx, sqlc.GetWorkspaceRunFileParams{
		RunKey:       runKey,
		RelativePath: relativePath,
	})
	if err != nil {
		return nil, wrapWorkspaceError(err, "get", "workspace_run_file")
	}
	mapped := fromSQLCFile(row)
	return &mapped, nil
}

func (s *store) ListFiles(ctx context.Context, filter ListFilesFilter) ([]WorkspaceRunFile, error) {
	rows, err := s.q.ListWorkspaceRunFiles(ctx, sqlc.ListWorkspaceRunFilesParams{
		RunKeyFilter: filter.RunKey,
		StateFilter:  filter.State,
		LimitCount:   int64(filter.Limit),
	})
	if err != nil {
		return nil, wrapWorkspaceError(err, "list", "workspace_run_file")
	}
	files := make([]WorkspaceRunFile, 0, len(rows))
	for _, row := range rows {
		files = append(files, fromSQLCFile(row))
	}
	return files, nil
}

func fromSQLCRun(row sqlc.WorkspaceRun) WorkspaceRun {
	return WorkspaceRun{
		ID:            row.ID,
		RunKey:        row.RunKey,
		DagKey:        row.DagKey,
		SourceRoot:    row.SourceRoot,
		WorkspacePath: row.WorkspacePath,
		Status:        row.Status,
		CreatedBy:     row.CreatedBy,
		UpdatedBy:     row.UpdatedBy,
		Metadata:      row.Metadata,
		CreatedAt:     sqlc.TimeValue(row.CreatedAt),
		UpdatedAt:     sqlc.TimeValue(row.UpdatedAt),
		FinishedAt:    sqlc.TimePtr(row.FinishedAt),
	}
}

func fromSQLCFile(row sqlc.WorkspaceRunFile) WorkspaceRunFile {
	return WorkspaceRunFile{
		ID:                 row.ID,
		RunKey:             row.RunKey,
		RelativePath:       row.RelativePath,
		BaselineSHA256:     row.BaselineSha256,
		WorkspaceSHA256:    row.WorkspaceSha256,
		SourceSHA256Before: row.SourceSha256Before,
		SourceSHA256After:  row.SourceSha256After,
		State:              row.State,
		LastError:          row.LastError,
		CreatedAt:          sqlc.TimeValue(row.CreatedAt),
		UpdatedAt:          sqlc.TimeValue(row.UpdatedAt),
	}
}

func wrapWorkspaceError(err error, operation, entity string) error {
	return platformdb.WrapStoreError(err, operation, entity)
}
