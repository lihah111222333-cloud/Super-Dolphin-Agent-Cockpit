package sharedfile

import (
	"context"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Reader { return &store{q: q} }

func (s *store) Get(ctx context.Context, path string) (*SharedFile, error) {
	row, err := s.q.GetSharedFile(ctx, path)
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "get", "shared_file")
	}
	mapped := fromSQLCRow(row)
	return &mapped, nil
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
