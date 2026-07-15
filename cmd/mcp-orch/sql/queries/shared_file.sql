-- name: InsertSharedFile :execrows
INSERT INTO shared_files (path, content, content_location, updated_by, created_at, updated_at)
VALUES (:path, :content, :content_location, :updated_by, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000));

-- name: UpdateSharedFile :execrows
UPDATE shared_files
SET content = :content,
    content_location = :content_location,
    updated_by = :updated_by,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE
    path = :path;

-- name: GetSharedFile :one
SELECT path, content, content_location, updated_by, created_at, updated_at
FROM shared_files
WHERE path = :path;

-- name: ListSharedFiles :many
SELECT path, content, content_location, updated_by, created_at, updated_at
FROM shared_files
WHERE (:prefix = '' OR substr(path, 1, :prefix_length) = :prefix)
ORDER BY updated_at DESC, path ASC
LIMIT :limit_count;

-- name: DeleteSharedFile :execrows
DELETE FROM shared_files
WHERE path = :path;
