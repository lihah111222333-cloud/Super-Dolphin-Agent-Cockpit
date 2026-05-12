-- name: GetAgentThreadByID :one
SELECT thread_id, name, prompt, model, cwd, status, port, pid, created_at, updated_at, finished_at, last_event_type, error_message, workspace_run_key, owner_thread_id, parent_agent_id, agent_type, agent_memory_scope, config_override, agent_key, prompt_version_id, pending_launch, manually_renamed,
       COALESCE((
            SELECT b.agent_id
            FROM agent_provider_binding b
            WHERE b.provider_thread_id = agent_threads.thread_id
               OR b.codex_thread_id = agent_threads.thread_id
            ORDER BY b.updated_at DESC
            LIMIT 1
        ), '') AS agent_id
FROM agent_threads
WHERE thread_id = $1
LIMIT 1;

-- name: GetAgentThreadByPort :one
SELECT thread_id, name, prompt, model, cwd, status, port, pid, created_at, updated_at, finished_at, last_event_type, error_message, workspace_run_key, owner_thread_id, parent_agent_id, agent_type, agent_memory_scope, config_override, agent_key, prompt_version_id, pending_launch, manually_renamed,
       COALESCE((
            SELECT b.agent_id
            FROM agent_provider_binding b
            WHERE b.provider_thread_id = agent_threads.thread_id
               OR b.codex_thread_id = agent_threads.thread_id
            ORDER BY b.updated_at DESC
            LIMIT 1
        ), '') AS agent_id
FROM agent_threads
WHERE port = $1 AND status = 'running'
ORDER BY updated_at DESC
LIMIT 1;

-- name: ListAgentThreads :many
SELECT thread_id, name, prompt, model, cwd, status, port, pid, created_at, updated_at, finished_at, last_event_type, error_message, workspace_run_key, owner_thread_id, parent_agent_id, agent_type, agent_memory_scope, config_override, agent_key, prompt_version_id, pending_launch, manually_renamed,
       COALESCE((
            SELECT b.agent_id
            FROM agent_provider_binding b
            WHERE b.provider_thread_id = agent_threads.thread_id
               OR b.codex_thread_id = agent_threads.thread_id
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
SELECT thread_id, name, prompt, model, cwd, status, port, pid, created_at, updated_at, finished_at, last_event_type, error_message, workspace_run_key, owner_thread_id, parent_agent_id, agent_type, agent_memory_scope, config_override, agent_key, prompt_version_id, pending_launch, manually_renamed,
       COALESCE((
            SELECT b.agent_id
            FROM agent_provider_binding b
            WHERE b.provider_thread_id = agent_threads.thread_id
               OR b.codex_thread_id = agent_threads.thread_id
            ORDER BY b.updated_at DESC
            LIMIT 1
        ), '') AS agent_id
FROM agent_threads
WHERE status = 'running'
ORDER BY created_at ASC;

-- name: ListRecoverableAgentThreads :many
SELECT thread_id, name, prompt, model, cwd, status, port, pid, created_at, updated_at, finished_at, last_event_type, error_message, workspace_run_key, owner_thread_id, parent_agent_id, agent_type, agent_memory_scope, config_override, agent_key, prompt_version_id, pending_launch, manually_renamed,
       COALESCE((
            SELECT b.agent_id
            FROM agent_provider_binding b
            WHERE b.provider_thread_id = agent_threads.thread_id
               OR b.codex_thread_id = agent_threads.thread_id
            ORDER BY b.updated_at DESC
            LIMIT 1
        ), '') AS agent_id
FROM agent_threads
WHERE status = 'created'
ORDER BY created_at ASC;

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
