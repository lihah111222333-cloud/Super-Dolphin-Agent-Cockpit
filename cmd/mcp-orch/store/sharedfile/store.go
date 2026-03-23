package sharedfile

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
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


func mapSharedFile(row sqlc.SharedFile) SharedFile {
	return SharedFile{
		Path:      row.Path,
		Content:   row.Content,
		UpdatedBy: row.UpdatedBy,
		CreatedAt: sqlc.TimeValue(row.CreatedAt),
		UpdatedAt: sqlc.TimeValue(row.UpdatedAt),
	}
}

func wrapSharedFileError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "shared_file")
}
