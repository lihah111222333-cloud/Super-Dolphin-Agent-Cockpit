-- name: UpsertSharedFile :one
INSERT INTO shared_files (path, content, updated_by, created_at, updated_at)
VALUES ($1, $2, $3, NOW(), NOW())
ON CONFLICT (path) DO UPDATE
SET content = EXCLUDED.content,
    updated_by = EXCLUDED.updated_by,
    updated_at = NOW()
RETURNING path, content, updated_by, created_at, updated_at;

-- name: GetSharedFile :one
SELECT path, content, updated_by, created_at, updated_at
FROM shared_files
WHERE path = $1;

-- name: ListSharedFiles :many
SELECT path, content, updated_by, created_at, updated_at
FROM shared_files
WHERE ($1::text = '' OR path ILIKE '%' || $1 || '%')
ORDER BY updated_at DESC, path ASC
LIMIT $2;

-- name: DeleteSharedFile :execrows
DELETE FROM shared_files
WHERE path = $1;
