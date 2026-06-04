-- name: GetAgentThreadByID :one
SELECT t.thread_id, t.name, t.prompt, t.model, t.cwd, t.status, t.port, t.pid, t.created_at, t.updated_at, t.finished_at, t.last_event_type, t.error_message, t.workspace_run_key, t.owner_thread_id, t.parent_agent_id, t.agent_type, t.agent_memory_scope, t.config_override, t.agent_key, t.prompt_version_id, t.pending_launch, t.manually_renamed,
       COALESCE(b.agent_id, '') AS agent_id
FROM agent_threads t
LEFT JOIN LATERAL (
    SELECT agent_id FROM agent_provider_binding
    WHERE provider_thread_id = t.thread_id OR codex_thread_id = t.thread_id
    ORDER BY updated_at DESC LIMIT 1
) b ON true
WHERE t.thread_id = $1
LIMIT 1;

-- name: GetAgentThreadByPort :one
SELECT t.thread_id, t.name, t.prompt, t.model, t.cwd, t.status, t.port, t.pid, t.created_at, t.updated_at, t.finished_at, t.last_event_type, t.error_message, t.workspace_run_key, t.owner_thread_id, t.parent_agent_id, t.agent_type, t.agent_memory_scope, t.config_override, t.agent_key, t.prompt_version_id, t.pending_launch, t.manually_renamed,
       COALESCE(b.agent_id, '') AS agent_id
FROM agent_threads t
LEFT JOIN LATERAL (
    SELECT agent_id FROM agent_provider_binding
    WHERE provider_thread_id = t.thread_id OR codex_thread_id = t.thread_id
    ORDER BY updated_at DESC LIMIT 1
) b ON true
WHERE t.port = $1 AND t.status = 'running'
ORDER BY t.updated_at DESC
LIMIT 1;

-- name: ListAgentThreads :many
SELECT t.thread_id, t.name, t.prompt, t.model, t.cwd, t.status, t.port, t.pid, t.created_at, t.updated_at, t.finished_at, t.last_event_type, t.error_message, t.workspace_run_key, t.owner_thread_id, t.parent_agent_id, t.agent_type, t.agent_memory_scope, t.config_override, t.agent_key, t.prompt_version_id, t.pending_launch, t.manually_renamed,
       COALESCE(b.agent_id, '') AS agent_id
FROM agent_threads t
LEFT JOIN LATERAL (
    SELECT agent_id FROM agent_provider_binding
    WHERE provider_thread_id = t.thread_id OR codex_thread_id = t.thread_id
    ORDER BY updated_at DESC LIMIT 1
) b ON true
ORDER BY t.created_at DESC;

-- name: ListRunningAgents :many
SELECT thread_id, port, pid, status
FROM agent_threads
WHERE status = 'running'
ORDER BY created_at DESC;

-- name: ListRunningAgentThreads :many
SELECT t.thread_id, t.name, t.prompt, t.model, t.cwd, t.status, t.port, t.pid, t.created_at, t.updated_at, t.finished_at, t.last_event_type, t.error_message, t.workspace_run_key, t.owner_thread_id, t.parent_agent_id, t.agent_type, t.agent_memory_scope, t.config_override, t.agent_key, t.prompt_version_id, t.pending_launch, t.manually_renamed,
       COALESCE(b.agent_id, '') AS agent_id
FROM agent_threads t
LEFT JOIN LATERAL (
    SELECT agent_id FROM agent_provider_binding
    WHERE provider_thread_id = t.thread_id OR codex_thread_id = t.thread_id
    ORDER BY updated_at DESC LIMIT 1
) b ON true
WHERE t.status = 'running'
ORDER BY t.created_at ASC;

-- name: ListRecoverableAgentThreads :many
SELECT t.thread_id, t.name, t.prompt, t.model, t.cwd, t.status, t.port, t.pid, t.created_at, t.updated_at, t.finished_at, t.last_event_type, t.error_message, t.workspace_run_key, t.owner_thread_id, t.parent_agent_id, t.agent_type, t.agent_memory_scope, t.config_override, t.agent_key, t.prompt_version_id, t.pending_launch, t.manually_renamed,
       COALESCE(b.agent_id, '') AS agent_id
FROM agent_threads t
LEFT JOIN LATERAL (
    SELECT agent_id FROM agent_provider_binding
    WHERE provider_thread_id = t.thread_id OR codex_thread_id = t.thread_id
    ORDER BY updated_at DESC LIMIT 1
) b ON true
WHERE t.status = 'created'
ORDER BY t.created_at ASC;

-- name: UpsertAgentThread :exec
INSERT INTO agent_threads (
    thread_id,
    name,
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
    config_override,
    agent_key,
    prompt_version_id,
    pending_launch,
    manually_renamed
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, COALESCE(sqlc.arg(config_override), '{}'::jsonb), sqlc.arg(agent_key), sqlc.narg(prompt_version_id), sqlc.arg(pending_launch), sqlc.arg(manually_renamed))
ON CONFLICT (thread_id) DO UPDATE
SET name = $2,
    prompt = $3,
    model = $4,
    cwd = $5,
    status = $6,
    port = $7,
    pid = $8,
    updated_at = $10,
    owner_thread_id = $11,
    parent_agent_id = $12,
    agent_type = $13,
    agent_memory_scope = $14,
    config_override = COALESCE(sqlc.arg(config_override), '{}'::jsonb),
    agent_key = sqlc.arg(agent_key),
    prompt_version_id = sqlc.narg(prompt_version_id),
    pending_launch = sqlc.arg(pending_launch),
    manually_renamed = sqlc.arg(manually_renamed);

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
SELECT DISTINCT ON (cwd) thread_id, cwd
FROM agent_threads
WHERE cwd <> ''
ORDER BY cwd, created_at DESC
LIMIT 100;

-- name: ListAgentThreadCwdsByPrefix :many
SELECT DISTINCT ON (cwd) thread_id, cwd
FROM agent_threads
WHERE cwd <> ''
  AND cwd LIKE $1 || '%'
ORDER BY cwd, created_at DESC
LIMIT 100;

-- name: UpdateAgentThreadLaunchResult :exec
-- Atomically clears pending_launch and stamps the router decision after the
-- Claude CLI has been spawned for this thread.
UPDATE agent_threads
SET agent_key = sqlc.arg(agent_key),
    prompt_version_id = sqlc.narg(prompt_version_id),
    pending_launch = false,
    updated_at = sqlc.arg(updated_at)
WHERE thread_id = sqlc.arg(thread_id);

-- name: CountChildAgentThreads :one
-- Returns the number of child agents belonging to the given parent.
-- Used to determine the next sequential suffix for child agent IDs.
SELECT COUNT(*)
FROM agent_threads
WHERE parent_agent_id = $1;

-- name: AgentThreadExists :one
SELECT EXISTS(
    SELECT 1
    FROM agent_threads
    WHERE thread_id = $1
);

-- name: CountAllThreads :one
SELECT COUNT(*) FROM agent_threads;

-- name: ListAgentThreadConfigsByIDs :many
SELECT thread_id, model, config_override
FROM agent_threads
WHERE thread_id IN (sqlc.slice('thread_ids'));

