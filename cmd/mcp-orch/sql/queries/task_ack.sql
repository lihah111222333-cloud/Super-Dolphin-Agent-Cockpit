-- name: UpsertTaskAck :one
INSERT INTO task_acks (
    ack_key, title, description, assigned_to, requested_by,
    priority, status, progress, ack_message, result_summary, metadata, due_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, max(0, min(sqlc.arg(progress), 100)), ?, ?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000))
ON CONFLICT (ack_key) DO UPDATE
SET title = EXCLUDED.title,
    description = EXCLUDED.description,
    assigned_to = EXCLUDED.assigned_to,
    requested_by = EXCLUDED.requested_by,
    priority = EXCLUDED.priority,
    status = EXCLUDED.status,
    progress = EXCLUDED.progress,
    ack_message = EXCLUDED.ack_message,
    result_summary = EXCLUDED.result_summary,
    metadata = EXCLUDED.metadata,
    due_at = EXCLUDED.due_at,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
RETURNING id, ack_key, title, description, assigned_to, requested_by, priority, status, progress, ack_message, result_summary, metadata, due_at, acked_at, started_at, finished_at, created_at, updated_at;

-- name: ListTaskAcks :many
SELECT id, ack_key, title, description, assigned_to, requested_by, priority, status, progress, ack_message, result_summary, metadata, due_at, acked_at, started_at, finished_at, created_at, updated_at
FROM task_acks
WHERE (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (sqlc.arg(priority_filter) = '' OR priority = sqlc.arg(priority_filter))
  AND (sqlc.arg(assigned_to_filter) = '' OR assigned_to = sqlc.arg(assigned_to_filter))
  AND (sqlc.arg(keyword) = ''
    OR ack_key LIKE '%' || sqlc.arg(keyword) || '%'
    OR title LIKE '%' || sqlc.arg(keyword) || '%'
    OR description LIKE '%' || sqlc.arg(keyword) || '%')
ORDER BY updated_at DESC, id DESC
LIMIT sqlc.arg(limit_count);
