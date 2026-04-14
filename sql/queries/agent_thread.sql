-- name: GetAgentThreadByID :one
SELECT thread_id, prompt, model, cwd, status, port, pid, created_at, updated_at, finished_at, last_event_type, error_message, workspace_run_key, owner_thread_id, parent_agent_id, agent_type, agent_memory_scope, config_override,
       COALESCE((
            SELECT b.agent_id
            FROM agent_provider_binding b
           WHERE b.provider_thread_id = agent_threads.thread_id
              OR b.codex_thread_id = agent_threads.thread_id
              OR (agent_threads.owner_thread_id <> '' AND (
                  b.provider_thread_id = agent_threads.owner_thread_id
                  OR b.codex_thread_id = agent_threads.owner_thread_id
              ))
           ORDER BY b.updated_at DESC
           LIMIT 1
       ), '') AS agent_id
FROM agent_threads
WHERE thread_id = $1
LIMIT 1;

-- name: GetAgentThreadByPort :one
SELECT thread_id, prompt, model, cwd, status, port, pid, created_at, updated_at, finished_at, last_event_type, error_message, workspace_run_key, owner_thread_id, parent_agent_id, agent_type, agent_memory_scope, config_override,
       COALESCE((
            SELECT b.agent_id
            FROM agent_provider_binding b
           WHERE b.provider_thread_id = agent_threads.thread_id
              OR b.codex_thread_id = agent_threads.thread_id
              OR (agent_threads.owner_thread_id <> '' AND (
                  b.provider_thread_id = agent_threads.owner_thread_id
                  OR b.codex_thread_id = agent_threads.owner_thread_id
              ))
           ORDER BY b.updated_at DESC
           LIMIT 1
       ), '') AS agent_id
FROM agent_threads
WHERE port = $1 AND status = 'running'
ORDER BY updated_at DESC
LIMIT 1;

-- name: ListAgentThreads :many
SELECT thread_id, prompt, model, cwd, status, port, pid, created_at, updated_at, finished_at, last_event_type, error_message, workspace_run_key, owner_thread_id, parent_agent_id, agent_type, agent_memory_scope, config_override,
       COALESCE((
            SELECT b.agent_id
            FROM agent_provider_binding b
           WHERE b.provider_thread_id = agent_threads.thread_id
              OR b.codex_thread_id = agent_threads.thread_id
              OR (agent_threads.owner_thread_id <> '' AND (
                  b.provider_thread_id = agent_threads.owner_thread_id
                  OR b.codex_thread_id = agent_threads.owner_thread_id
              ))
           ORDER BY b.updated_at DESC
           LIMIT 1
       ), '') AS agent_id
FROM agent_threads
ORDER BY created_at DESC;

-- name: ListRunningAgents :many
SELECT thread_id, port, pid, status
FROM agent_threads
WHERE status = 'running'
ORDER BY created_at DESC;

-- name: ListRunningAgentThreads :many
SELECT thread_id, prompt, model, cwd, status, port, pid, created_at, updated_at, finished_at, last_event_type, error_message, workspace_run_key, owner_thread_id, parent_agent_id, agent_type, agent_memory_scope, config_override,
       COALESCE((
            SELECT b.agent_id
            FROM agent_provider_binding b
           WHERE b.provider_thread_id = agent_threads.thread_id
              OR b.codex_thread_id = agent_threads.thread_id
              OR (agent_threads.owner_thread_id <> '' AND (
                  b.provider_thread_id = agent_threads.owner_thread_id
                  OR b.codex_thread_id = agent_threads.owner_thread_id
              ))
           ORDER BY b.updated_at DESC
           LIMIT 1
       ), '') AS agent_id
FROM agent_threads
WHERE status = 'running'
ORDER BY created_at ASC;

-- name: ListRecoverableAgentThreads :many
SELECT thread_id, prompt, model, cwd, status, port, pid, created_at, updated_at, finished_at, last_event_type, error_message, workspace_run_key, owner_thread_id, parent_agent_id, agent_type, agent_memory_scope, config_override,
       COALESCE((
            SELECT b.agent_id
            FROM agent_provider_binding b
           WHERE b.provider_thread_id = agent_threads.thread_id
              OR b.codex_thread_id = agent_threads.thread_id
              OR (agent_threads.owner_thread_id <> '' AND (
                  b.provider_thread_id = agent_threads.owner_thread_id
                  OR b.codex_thread_id = agent_threads.owner_thread_id
              ))
           ORDER BY b.updated_at DESC
           LIMIT 1
       ), '') AS agent_id
FROM agent_threads
WHERE status = 'created'
ORDER BY created_at ASC;

-- name: UpsertAgentThread :exec
INSERT INTO agent_threads (
    thread_id,
    prompt,
    model,
    cwd,
    status,
    port,
    pid,
    created_at,
    updated_at,
    owner_thread_id,
    parent_agent_id,
    agent_type,
    agent_memory_scope,
    config_override
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, COALESCE(sqlc.arg(config_override), '{}'::jsonb))
ON CONFLICT (thread_id) DO UPDATE
SET prompt = $2,
    model = $3,
    cwd = $4,
    status = $5,
    port = $6,
    pid = $7,
    updated_at = $9,
    owner_thread_id = $10,
    parent_agent_id = $11,
    agent_type = $12,
    agent_memory_scope = $13,
    config_override = COALESCE(sqlc.arg(config_override), '{}'::jsonb);

-- name: UpdateAgentThreadStatus :exec
UPDATE agent_threads
SET status = $2,
    updated_at = $3
WHERE thread_id = $1;

-- name: DeleteAgentThreadByID :exec
DELETE FROM agent_threads
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
