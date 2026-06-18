-- name: UpsertDatasourceV2DocumentImporting :one
INSERT INTO datasource_v2_documents (
    source_path,
    file_name,
    extension,
    size_bytes,
    status,
    error_message,
    content_hash,
    chunk_count,
    total_chars,
    updated_at
)
VALUES (
    sqlc.arg(source_path),
    sqlc.arg(file_name),
    sqlc.arg(extension),
    sqlc.arg(size_bytes),
    'importing',
    NULL,
    NULL,
    0,
    0,
    (CAST(strftime('%s','now') AS INTEGER) * 1000)
)
ON CONFLICT (source_path) DO UPDATE
SET file_name = EXCLUDED.file_name,
    extension = EXCLUDED.extension,
    size_bytes = EXCLUDED.size_bytes,
    status = 'importing',
    error_message = NULL,
    content_hash = NULL,
    chunk_count = 0,
    total_chars = 0,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
RETURNING id, source_path, file_name, extension, size_bytes, content_hash, chunk_count, total_chars, status, error_message, created_at, updated_at;

-- name: DeleteDatasourceV2ChunksByDocumentID :execrows
DELETE FROM datasource_v2_text_chunks
WHERE document_id = sqlc.arg(document_id);

-- name: InsertDatasourceV2Chunk :exec
INSERT INTO datasource_v2_text_chunks (
    document_id,
    chunk_index,
    content,
    char_count,
    byte_count
)
VALUES (
    sqlc.arg(document_id),
    sqlc.arg(chunk_index),
    sqlc.arg(content),
    sqlc.arg(char_count),
    sqlc.arg(byte_count)
);

-- name: MarkDatasourceV2DocumentReady :one
UPDATE datasource_v2_documents
SET content_hash = sqlc.arg(content_hash),
    chunk_count = sqlc.arg(chunk_count),
    total_chars = sqlc.arg(total_chars),
    status = 'ready',
    error_message = NULL,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE id = sqlc.arg(id)
RETURNING id, source_path, file_name, extension, size_bytes, content_hash, chunk_count, total_chars, status, error_message, created_at, updated_at;

-- name: ListDatasourceV2Documents :many
SELECT id, source_path, file_name, extension, size_bytes, content_hash, chunk_count, total_chars, status, error_message, created_at, updated_at
FROM datasource_v2_documents
WHERE (
    sqlc.arg(keyword) = ''
    OR source_path LIKE '%' || sqlc.arg(keyword) || '%'
    OR file_name LIKE '%' || sqlc.arg(keyword) || '%'
    OR status LIKE '%' || sqlc.arg(keyword) || '%'
)
ORDER BY updated_at DESC, id DESC
LIMIT sqlc.arg(limit);

-- name: GetDatasourceV2Document :one
SELECT id, source_path, file_name, extension, size_bytes, content_hash, chunk_count, total_chars, status, error_message, created_at, updated_at
FROM datasource_v2_documents
WHERE id = sqlc.arg(id);

-- name: ListDatasourceV2Chunks :many
SELECT id, document_id, chunk_index, content, char_count, byte_count, created_at
FROM datasource_v2_text_chunks
WHERE document_id = sqlc.arg(document_id)
ORDER BY chunk_index ASC;

-- name: UpdateDatasourceV2DocumentMetadata :one
UPDATE datasource_v2_documents
SET source_path = sqlc.arg(source_path),
    file_name = sqlc.arg(file_name),
    extension = sqlc.arg(extension),
    size_bytes = sqlc.arg(size_bytes),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE id = sqlc.arg(id)
RETURNING id, source_path, file_name, extension, size_bytes, content_hash, chunk_count, total_chars, status, error_message, created_at, updated_at;

-- name: DeleteDatasourceV2Document :execrows
DELETE FROM datasource_v2_documents
WHERE id = sqlc.arg(id);
