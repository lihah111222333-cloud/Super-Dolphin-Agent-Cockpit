-- datasource.sql - sqlc queries for datasource_documents.

-- name: UpsertDatasourceDocument :execrows
INSERT INTO datasource_documents (
    workspace_root, name, extension, size_bytes, stored_path, content, updated_at
) VALUES (
    sqlc.arg(workspace_root), sqlc.arg(name), sqlc.arg(extension), sqlc.arg(size_bytes),
    sqlc.arg(stored_path), sqlc.arg(content), CAST(strftime('%s','now') AS INTEGER) * 1000
)
ON CONFLICT (workspace_root, name) DO UPDATE SET
    extension = excluded.extension,
    size_bytes = excluded.size_bytes,
    stored_path = excluded.stored_path,
    content = excluded.content,
    updated_at = CAST(strftime('%s','now') AS INTEGER) * 1000;

-- name: ListDatasourceDocuments :many
SELECT workspace_root, name, extension, size_bytes, stored_path, content
FROM datasource_documents
WHERE workspace_root = ?
ORDER BY name ASC;

-- name: DeleteDatasourceDocument :execrows
DELETE FROM datasource_documents
WHERE workspace_root = ? AND name = ?;
