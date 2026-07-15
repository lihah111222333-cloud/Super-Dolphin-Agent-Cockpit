-- name: InsertTaskAck :execrows
INSERT INTO task_acks (
    ack_key, title, description, assigned_to, requested_by,
    priority, status, progress, ack_message, result_summary, metadata, due_at, created_at, updated_at
) VALUES (
    :ack_key, :title, :description, :assigned_to, :requested_by,
    :priority, :status, max(0, min(:progress, 100)), :ack_message,
    :result_summary, :metadata, :due_at,
    (CAST(strftime('%s','now') AS INTEGER) * 1000),
    (CAST(strftime('%s','now') AS INTEGER) * 1000)
);

-- name: UpdateTaskAck :execrows
UPDATE task_acks
SET title = :title,
    description = :description,
    assigned_to = :assigned_to,
    requested_by = :requested_by,
    priority = :priority,
    status = :status,
    progress = max(0, min(:progress, 100)),
    ack_message = :ack_message,
    result_summary = :result_summary,
    metadata = :metadata,
    due_at = :due_at,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE
    ack_key = :ack_key;

-- name: GetTaskAck :one
SELECT id, ack_key, title, description, assigned_to, requested_by, priority, status, progress,
       ack_message, result_summary, metadata, due_at, acked_at, started_at, finished_at,
       created_at, updated_at
FROM task_acks
WHERE ack_key = :ack_key;

-- name: ListTaskAcks :many
SELECT id, ack_key, title, description, assigned_to, requested_by, priority, status, progress, ack_message, result_summary, CAST('{}' AS BLOB) AS metadata, due_at, acked_at, started_at, finished_at, created_at, updated_at
FROM task_acks
WHERE (:status_filter = '' OR status = :status_filter)
  AND (:priority_filter = '' OR priority = :priority_filter)
  AND (:assigned_to_filter = '' OR assigned_to = :assigned_to_filter)
  AND (:keyword = ''
    OR ack_key LIKE :keyword_pattern
    OR title LIKE :keyword_pattern
    OR description LIKE :keyword_pattern)
ORDER BY updated_at DESC, id DESC
LIMIT :limit_count;
