-- name: GetAgentThreadByPort :one
SELECT thread_id, prompt, model, cwd, status, port, pid, created_at, updated_at, finished_at, last_event_type, error_message, workspace_run_key, owner_thread_id
FROM agent_threads
WHERE port = $1 AND status = 'running'
ORDER BY updated_at DESC
LIMIT 1;

-- name: ListRunningAgents :many
SELECT thread_id, port, pid, status
FROM agent_threads
WHERE status = 'running'
ORDER BY created_at DESC;

-- name: ListRunningAgentThreads :many
SELECT thread_id, prompt, model, cwd, status, port, pid, created_at, updated_at, finished_at, last_event_type, error_message, workspace_run_key, owner_thread_id
FROM agent_threads
WHERE status = 'running'
ORDER BY created_at ASC;

-- name: ListRecoverableAgentThreads :many
SELECT thread_id, prompt, model, cwd, status, port, pid, created_at, updated_at, finished_at, last_event_type, error_message, workspace_run_key, owner_thread_id
FROM agent_threads
WHERE status = 'created'
ORDER BY created_at ASC;

-- name: UpsertAgentThread :exec
INSERT INTO agent_threads (thread_id, prompt, model, cwd, status, port, pid, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (thread_id) DO UPDATE
SET status = $5,
    cwd = $4,
    updated_at = $9;

-- name: UpdateAgentThreadStatus :exec
UPDATE agent_threads
SET status = $2,
    updated_at = $3
WHERE thread_id = $1;

-- name: ResetRunningAgentThreads :exec
UPDATE agent_threads
SET status = 'created'
WHERE status = 'running';

-- name: ExpireStaleAgentThreads :execrows
UPDATE agent_threads
SET status = 'expired',
    updated_at = $1
WHERE status IN ('created', 'running')
  AND updated_at < $2;

-- name: AgentThreadRunningExists :one
SELECT EXISTS(
    SELECT 1
    FROM agent_threads
    WHERE thread_id = $1 AND status = 'running'
);

-- name: ListAgentThreadCwds :many
SELECT thread_id, cwd
FROM agent_threads
WHERE cwd <> ''
ORDER BY created_at DESC;

-- name: ListAgentThreadCwdsByPrefix :many
SELECT thread_id, cwd
FROM agent_threads
WHERE cwd <> ''
  AND cwd LIKE $1 || '%'
ORDER BY created_at DESC;
