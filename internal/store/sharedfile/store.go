package sharedfile

import (
	"context"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	sharedfilepath "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilepath"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type querier interface {
	GetSharedFile(ctx context.Context, path string) (sqlc.SharedFile, error)
	ListSharedFiles(ctx context.Context, arg sqlc.ListSharedFilesParams) ([]sqlc.SharedFile, error)
	DeleteSharedFile(ctx context.Context, path string) (int64, error)
	UpsertSharedFile(ctx context.Context, arg sqlc.UpsertSharedFileParams) (sqlc.SharedFile, error)
}

type store struct {
	q querier
}

func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

func (s *store) Get(ctx context.Context, path string) (*SharedFile, error) {
	cleaned, err := sharedfilepath.ValidateReadPath(path)
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "get", "shared_file")
	}
	row, err := s.q.GetSharedFile(ctx, cleaned)
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "get", "shared_file")
	}
	mapped := fromSQLCRow(row)
	return &mapped, nil
}

func (s *store) Upsert(ctx context.Context, params UpsertParams) (*SharedFile, error) {
	cleaned, err := sharedfilepath.ValidateWritePath(params.Path)
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "upsert", "shared_file")
	}
	row, err := s.q.UpsertSharedFile(ctx, sqlc.UpsertSharedFileParams{
		Path:      cleaned,
		Content:   params.Content,
		UpdatedBy: params.UpdatedBy,
	})
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "upsert", "shared_file")
	}
	mapped := fromSQLCRow(row)
	return &mapped, nil
}

func (s *store) Delete(ctx context.Context, path string) (int64, error) {
	cleaned, err := sharedfilepath.ValidateReadPath(path)
	if err != nil {
		return 0, platformdb.WrapStoreError(err, "delete", "shared_file")
	}
	count, err := s.q.DeleteSharedFile(ctx, cleaned)
	if err != nil {
		return 0, platformdb.WrapStoreError(err, "delete", "shared_file")
	}
	return count, nil
}

func (s *store) List(ctx context.Context, filter ListFilter) ([]SharedFile, error) {
	rows, err := s.q.ListSharedFiles(ctx, sqlc.ListSharedFilesParams{
		Column1: filter.Prefix,
		Limit:   filter.Limit,
	})
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "list", "shared_file")
	}
	files := make([]SharedFile, 0, len(rows))
	for _, row := range rows {
		files = append(files, fromSQLCListRow(row))
	}
	return files, nil
}

func fromSQLCRow(row sqlc.SharedFile) SharedFile {
	return SharedFile{
		Path:      row.Path,
		Content:   row.Content,
		UpdatedBy: row.UpdatedBy,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}

func fromSQLCListRow(row sqlc.SharedFile) SharedFile {
	return SharedFile{
		Path:      row.Path,
		Content:   row.Content,
		UpdatedBy: row.UpdatedBy,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}
