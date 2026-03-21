package sharedfile

import (
	"context"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

func (s *store) Upsert(ctx context.Context, params UpsertParams) (*SharedFile, error) {
	row, err := s.q.UpsertSharedFile(ctx, sqlc.UpsertSharedFileParams{
		Path:      params.Path,
		Content:   params.Content,
		UpdatedBy: params.UpdatedBy,
	})
	if err != nil {
		return nil, wrapSharedFileError(err, "upsert")
	}
	result := mapSharedFile(row)
	return &result, nil
}

func (s *store) Get(ctx context.Context, path string) (*SharedFile, error) {
	row, err := s.q.GetSharedFile(ctx, path)
	if err != nil {
		return nil, wrapSharedFileError(err, "get")
	}
	result := mapSharedFile(row)
	return &result, nil
}

func (s *store) List(ctx context.Context, filter ListFilter) ([]SharedFile, error) {
	rows, err := s.q.ListSharedFiles(ctx, sqlc.ListSharedFilesParams{
		Prefix: filter.Prefix,
		Limit:  filter.Limit,
	})
	if err != nil {
		return nil, wrapSharedFileError(err, "list")
	}
	result := make([]SharedFile, len(rows))
	for i, row := range rows {
		result[i] = mapSharedFile(row)
	}
	return result, nil
}

func (s *store) Delete(ctx context.Context, path string) (int64, error) {
	count, err := s.q.DeleteSharedFile(ctx, path)
	if err != nil {
		return 0, wrapSharedFileError(err, "delete")
	}
	return count, nil
}

func mapSharedFile(row sqlc.SharedFile) SharedFile {
	return SharedFile{
		Path:      row.Path,
		Content:   row.Content,
		UpdatedBy: row.UpdatedBy,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}

func wrapSharedFileError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "shared_file")
}
