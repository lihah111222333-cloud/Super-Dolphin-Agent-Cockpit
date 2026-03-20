package sqlc

import "context"

const (
	upsertSharedFileSQL = `INSERT INTO shared_files (path, content, updated_by, created_at, updated_at) VALUES ($1, $2, $3, NOW(), NOW()) ON CONFLICT (path) DO UPDATE SET content = EXCLUDED.content, updated_by = EXCLUDED.updated_by, updated_at = NOW() RETURNING path, content, updated_by, created_at, updated_at;`
	getSharedFileSQL    = `SELECT path, content, updated_by, created_at, updated_at FROM shared_files WHERE path = $1;`
	listSharedFilesSQL  = `SELECT path, content, updated_by, created_at, updated_at FROM shared_files WHERE ($1::text = '' OR path ILIKE '%' || $1 || '%') ORDER BY updated_at DESC, path ASC LIMIT $2;`
	deleteSharedFileSQL = `DELETE FROM shared_files WHERE path = $1;`
)

func scanSharedFile(row rowScanner) (SharedFile, error) {
	var item SharedFile
	err := row.Scan(&item.Path, &item.Content, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func (q *Queries) UpsertSharedFile(ctx context.Context, arg UpsertSharedFileParams) (SharedFile, error) {
	return queryOne(ctx, q, upsertSharedFileSQL, scanSharedFile, arg.Path, arg.Content, arg.UpdatedBy)
}

func (q *Queries) GetSharedFile(ctx context.Context, path string) (SharedFile, error) {
	return queryOne(ctx, q, getSharedFileSQL, scanSharedFile, path)
}

func (q *Queries) ListSharedFiles(ctx context.Context, arg ListSharedFilesParams) ([]SharedFile, error) {
	return queryMany(ctx, q, listSharedFilesSQL, scanSharedFile, arg.Prefix, arg.Limit)
}

func (q *Queries) DeleteSharedFile(ctx context.Context, path string) (int64, error) {
	return q.execRows(ctx, deleteSharedFileSQL, path)
}
