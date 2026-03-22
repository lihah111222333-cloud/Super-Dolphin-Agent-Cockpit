-- name: UpsertTaskAck :one
INSERT INTO task_acks (
    ack_key, title, description, assigned_to, requested_by,
    priority, status, progress, ack_message, result_summary, metadata, due_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, GREATEST(0, LEAST($8, 100)), $9, $10, $11::jsonb, $12::timestamptz)
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
    updated_at = NOW()
RETURNING id, ack_key, title, description, assigned_to, requested_by, priority, status, progress, ack_message, result_summary, metadata, due_at, acked_at, started_at, finished_at, created_at, updated_at;

-- name: ListTaskAcks :many
SELECT id, ack_key, title, description, assigned_to, requested_by, priority, status, progress, ack_message, result_summary, metadata, due_at, acked_at, started_at, finished_at, created_at, updated_at
FROM task_acks
WHERE ($1::text = '' OR status = $1)
  AND ($2::text = '' OR priority = $2)
  AND ($3::text = '' OR assigned_to = $3)
  AND ($4::text = ''
    OR ack_key ILIKE '%' || $4 || '%'
    OR title ILIKE '%' || $4 || '%'
    OR description ILIKE '%' || $4 || '%')
ORDER BY updated_at DESC, id DESC
LIMIT $5;
