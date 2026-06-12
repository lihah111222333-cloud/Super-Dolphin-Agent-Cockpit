-- name: UpsertSharedFile :one
INSERT INTO shared_files (path, content, updated_by, created_at, updated_at)
VALUES (?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000))
ON CONFLICT (path) DO UPDATE
SET content = EXCLUDED.content,
    updated_by = EXCLUDED.updated_by,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
RETURNING path, content, updated_by, created_at, updated_at;

-- name: GetSharedFile :one
SELECT path, content, updated_by, created_at, updated_at
FROM shared_files
WHERE path = ?;

-- name: ListSharedFiles :many
SELECT path, content, updated_by, created_at, updated_at
FROM shared_files
WHERE (sqlc.arg(prefix) = '' OR path LIKE '%' || sqlc.arg(prefix) || '%')
ORDER BY updated_at DESC, path ASC
LIMIT sqlc.arg(limit_count);

-- name: DeleteSharedFile :execrows
DELETE FROM shared_files
WHERE path = ?;
