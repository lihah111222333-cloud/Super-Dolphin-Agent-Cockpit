-- name: UpsertTaskDag :one
INSERT INTO task_dags (dag_key, title, description, status, created_by, metadata)
VALUES ($1, $2, $3, $4, $5, $6::jsonb)
ON CONFLICT (dag_key) DO UPDATE
SET title = EXCLUDED.title,
    description = EXCLUDED.description,
    status = EXCLUDED.status,
    created_by = EXCLUDED.created_by,
    metadata = EXCLUDED.metadata,
    updated_at = NOW()
RETURNING id, dag_key, title, description, status, created_by, metadata, started_at, finished_at, created_at, updated_at;

-- name: ListTaskDags :many
SELECT id, dag_key, title, description, status, created_by, metadata, started_at, finished_at, created_at, updated_at
FROM task_dags
WHERE ($1::text = '' OR status = $1)
  AND ($2::text = ''
    OR dag_key ILIKE '%' || $2 || '%'
    OR title ILIKE '%' || $2 || '%'
    OR description ILIKE '%' || $2 || '%')
ORDER BY updated_at DESC, id DESC
LIMIT $3;

-- name: GetTaskDag :one
SELECT id, dag_key, title, description, status, created_by, metadata, started_at, finished_at, created_at, updated_at
FROM task_dags
WHERE dag_key = $1;

-- name: GetTaskDagForUpdate :one
SELECT id, dag_key, title, description, status, created_by, metadata, started_at, finished_at, created_at, updated_at
FROM task_dags
WHERE dag_key = $1
FOR UPDATE;
