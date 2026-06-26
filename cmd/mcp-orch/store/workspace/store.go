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

// NewStore 创建 workspace 存储实现，所有写入通过 sqlctx retry/事务 helper 统一收口。
func NewStore(db sqlc.DBTX) Store { return &store{db: db, q: sqlc.New(db)} }

// WithTx 用 SQLite IMMEDIATE 事务重绑查询集，保证同一次 workspace 操作内的 run/file 写入一致。
func (s *store) WithTx(ctx context.Context, fn func(txStore Store) error) error {
	err := sqlctx.WithImmediateTx(ctx, s.db, s.q, func(txq *sqlc.Queries, tx sqlc.DBTX) error {
		return fn(&store{db: tx, q: txq})
	})
	return wrapWorkspaceError(err, "with_tx", "workspace")
}

// UpsertRun 创建或更新 workspace run，并在 SQLite busy 时按写重试策略重放整个 upsert。
func (s *store) UpsertRun(ctx context.Context, run WorkspaceRun) (*WorkspaceRun, error) {
	var mapped WorkspaceRun
	err := sqlctx.WithWriteRetry(ctx, func() error {
		next, err := s.q.UpsertWorkspaceRun(ctx, sqlc.UpsertWorkspaceRunParams{
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
			return wrapWorkspaceError(err, "upsert", "workspace_run")
		}
		mapped = fromSQLCUpsertRun(next)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &mapped, nil
}

// GetRun 按 run_key 读取 workspace run；未命中会经 wrapWorkspaceError 归一成存储层 not found。
func (s *store) GetRun(ctx context.Context, runKey string) (*WorkspaceRun, error) {
	row, err := s.q.GetWorkspaceRun(ctx, sqlc.GetWorkspaceRunParams{RunKey: runKey})
	if err != nil {
		return nil, wrapWorkspaceError(err, "get", "workspace_run")
	}
	mapped := fromSQLCGetRun(row)
	return &mapped, nil
}

// ListRuns 按 status/dag_key 过滤运行记录，limit 直接下推到 SQL，调用方负责选择分页窗口。
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
		runs = append(runs, fromSQLCListRun(row))
	}
	return runs, nil
}

// UpdateRunStatus 覆写 workspace run 状态并保留调用方传入的 metadata，不做 expected-status CAS。
func (s *store) UpdateRunStatus(ctx context.Context, input UpdateRunStatusInput) (*WorkspaceRun, error) {
	var mapped WorkspaceRun
	err := sqlctx.WithWriteRetry(ctx, func() error {
		next, err := s.q.UpdateWorkspaceRunStatus(ctx, sqlc.UpdateWorkspaceRunStatusParams{
			NewStatus: input.Status,
			UpdatedBy: input.UpdatedBy,
			Metadata:  input.Metadata,
			RunKey:    input.RunKey,
		})
		if err != nil {
			return wrapWorkspaceError(err, "update_status", "workspace_run")
		}
		mapped = fromSQLCUpdateRunStatus(next)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &mapped, nil
}

// TransitionRunStatus 以 expected status 作为 CAS fence 推进 workspace run。
// 状态已被其它路径改写时由 sqlc 返回 0 行/未找到错误，调用方据此判断并发冲突。
func (s *store) TransitionRunStatus(ctx context.Context, input TransitionRunStatusInput) (*WorkspaceRun, error) {
	var mapped WorkspaceRun
	err := sqlctx.WithWriteRetry(ctx, func() error {
		next, err := s.q.TransitionWorkspaceRunStatus(ctx, sqlc.TransitionWorkspaceRunStatusParams{
			NewStatus:      input.Status,
			UpdatedBy:      input.UpdatedBy,
			Metadata:       input.Metadata,
			RunKey:         input.RunKey,
			ExpectedStatus: input.FromStatus,
		})
		if err != nil {
			return wrapWorkspaceError(err, "transition_status", "workspace_run")
		}
		mapped = fromSQLCTransitionRunStatus(next)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &mapped, nil
}

// UpsertFile 写入 workspace 文件快照，保留源文件修改前后 hash 供合并/回滚判断。
func (s *store) UpsertFile(ctx context.Context, file WorkspaceRunFile) (*WorkspaceRunFile, error) {
	var row sqlc.WorkspaceRunFile
	err := sqlctx.WithWriteRetry(ctx, func() error {
		next, err := s.q.UpsertWorkspaceRunFile(ctx, sqlc.UpsertWorkspaceRunFileParams{
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
			return wrapWorkspaceError(err, "upsert", "workspace_run_file")
		}
		row = next
		return nil
	})
	if err != nil {
		return nil, err
	}
	mapped := fromSQLCFile(row)
	return &mapped, nil
}

// GetFile 按 run_key 与相对路径读取文件快照；相对路径是 workspace 合并/回滚的稳定定位键。
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

// ListFiles 按 run_key/state 过滤文件快照，返回值不读取文件内容，只暴露合并状态与哈希。
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
	return fromSQLCRunRaw(row.ID, row.RunKey, row.DagKey, row.SourceRoot, row.WorkspacePath, row.Status, row.CreatedBy, row.UpdatedBy, row.Metadata, row.CreatedAt, row.UpdatedAt, row.FinishedAt)
}

func fromSQLCListRun(row sqlc.ListWorkspaceRunsRow) WorkspaceRun {
	return fromSQLCRunRaw(row.ID, row.RunKey, row.DagKey, row.SourceRoot, row.WorkspacePath, row.Status, row.CreatedBy, row.UpdatedBy, row.Metadata, row.CreatedAt, row.UpdatedAt, row.FinishedAt)
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
