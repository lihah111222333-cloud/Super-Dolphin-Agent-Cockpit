-- name: GetAgentThreadByID :one
SELECT t.thread_id, t.name, t.prompt, t.model, t.cwd, t.status, t.port, t.pid, t.created_at, t.updated_at, t.finished_at, t.last_event_type, t.error_message, t.workspace_run_key, t.owner_thread_id, t.parent_agent_id, t.agent_type, t.agent_memory_scope, t.config_override, t.agent_key, t.prompt_version_id, t.pending_launch, t.manually_renamed,
       COALESCE(b.agent_id, '') AS agent_id
FROM agent_threads t
LEFT JOIN (
    SELECT agent_id, provider_thread_id, codex_thread_id, updated_at FROM agent_provider_binding
) b ON (b.provider_thread_id = t.thread_id OR b.codex_thread_id = t.thread_id)
WHERE t.thread_id = ?
ORDER BY b.updated_at DESC
LIMIT 1;

-- name: GetAgentThreadByPort :one
SELECT t.thread_id, t.name, t.prompt, t.model, t.cwd, t.status, t.port, t.pid, t.created_at, t.updated_at, t.finished_at, t.last_event_type, t.error_message, t.workspace_run_key, t.owner_thread_id, t.parent_agent_id, t.agent_type, t.agent_memory_scope, t.config_override, t.agent_key, t.prompt_version_id, t.pending_launch, t.manually_renamed,
       COALESCE(b.agent_id, '') AS agent_id
FROM agent_threads t
LEFT JOIN (
    SELECT agent_id, provider_thread_id, codex_thread_id, updated_at FROM agent_provider_binding
) b ON (b.provider_thread_id = t.thread_id OR b.codex_thread_id = t.thread_id)
WHERE t.port = ? AND t.status = 'running'
ORDER BY t.updated_at DESC
LIMIT 1;

-- name: ListAgentThreads :many
SELECT t.thread_id, t.name, t.prompt, t.model, t.cwd, t.status, t.port, t.pid, t.created_at, t.updated_at, t.finished_at, t.last_event_type, t.error_message, t.workspace_run_key, t.owner_thread_id, t.parent_agent_id, t.agent_type, t.agent_memory_scope, t.config_override, t.agent_key, t.prompt_version_id, t.pending_launch, t.manually_renamed,
       COALESCE(b.agent_id, '') AS agent_id
FROM agent_threads t
LEFT JOIN (
    SELECT agent_id, provider_thread_id, codex_thread_id FROM agent_provider_binding
) b ON (b.provider_thread_id = t.thread_id OR b.codex_thread_id = t.thread_id)
ORDER BY t.created_at DESC;

-- name: ListAgentThreadsPage :many
SELECT t.thread_id, t.name, t.prompt, t.model, t.cwd, t.status, t.port, t.pid, t.created_at, t.updated_at, t.finished_at, t.last_event_type, t.error_message, t.workspace_run_key, t.owner_thread_id, t.parent_agent_id, t.agent_type, t.agent_memory_scope, t.config_override, t.agent_key, t.prompt_version_id, t.pending_launch, t.manually_renamed,
       COALESCE(b.agent_id, '') AS agent_id
FROM agent_threads t
LEFT JOIN (
    SELECT agent_id, provider_thread_id, codex_thread_id FROM agent_provider_binding
) b ON (b.provider_thread_id = t.thread_id OR b.codex_thread_id = t.thread_id)
WHERE sqlc.arg(cursor_thread_id) = ''
   OR t.created_at < sqlc.arg(cursor_created_at)
   OR (t.created_at = sqlc.arg(cursor_created_at) AND t.thread_id < sqlc.arg(cursor_thread_id))
ORDER BY t.created_at DESC, t.thread_id DESC
LIMIT sqlc.arg(limit) + 1;

-- name: ListLoadedAgentThreadsPage :many
SELECT t.thread_id, t.name, t.prompt, t.model, t.cwd, t.status, t.port, t.pid, t.created_at, t.updated_at, t.finished_at, t.last_event_type, t.error_message, t.workspace_run_key, t.owner_thread_id, t.parent_agent_id, t.agent_type, t.agent_memory_scope, t.config_override, t.agent_key, t.prompt_version_id, t.pending_launch, t.manually_renamed,
       COALESCE(b.agent_id, '') AS agent_id
FROM agent_threads t
LEFT JOIN (
    SELECT agent_id, provider_thread_id, codex_thread_id FROM agent_provider_binding
) b ON (b.provider_thread_id = t.thread_id OR b.codex_thread_id = t.thread_id)
WHERE t.status = 'created'
  AND (
      sqlc.arg(cursor_thread_id) = ''
      OR t.created_at < sqlc.arg(cursor_created_at)
      OR (t.created_at = sqlc.arg(cursor_created_at) AND t.thread_id < sqlc.arg(cursor_thread_id))
  )
ORDER BY t.created_at DESC, t.thread_id DESC
LIMIT sqlc.arg(limit) + 1;

-- name: CountActiveAgentThreads :one
SELECT COUNT(*)
FROM agent_threads
WHERE TRIM(COALESCE(status, '')) NOT IN ('', 'stopped', 'failed', 'archived');

-- name: ListRunningAgents :many
SELECT thread_id, port, pid, status
FROM agent_threads
WHERE status = 'running'
ORDER BY created_at DESC;

-- name: ListRunningAgentThreads :many
SELECT t.thread_id, t.name, t.prompt, t.model, t.cwd, t.status, t.port, t.pid, t.created_at, t.updated_at, t.finished_at, t.last_event_type, t.error_message, t.workspace_run_key, t.owner_thread_id, t.parent_agent_id, t.agent_type, t.agent_memory_scope, t.config_override, t.agent_key, t.prompt_version_id, t.pending_launch, t.manually_renamed,
       COALESCE(b.agent_id, '') AS agent_id
FROM agent_threads t
LEFT JOIN (
    SELECT agent_id, provider_thread_id, codex_thread_id FROM agent_provider_binding
) b ON (b.provider_thread_id = t.thread_id OR b.codex_thread_id = t.thread_id)
WHERE t.status = 'running'
ORDER BY t.created_at ASC;

-- name: ListRecoverableAgentThreads :many
SELECT t.thread_id, t.name, t.prompt, t.model, t.cwd, t.status, t.port, t.pid, t.created_at, t.updated_at, t.finished_at, t.last_event_type, t.error_message, t.workspace_run_key, t.owner_thread_id, t.parent_agent_id, t.agent_type, t.agent_memory_scope, t.config_override, t.agent_key, t.prompt_version_id, t.pending_launch, t.manually_renamed,
       COALESCE(b.agent_id, '') AS agent_id
FROM agent_threads t
LEFT JOIN (
    SELECT agent_id, provider_thread_id, codex_thread_id FROM agent_provider_binding
) b ON (b.provider_thread_id = t.thread_id OR b.codex_thread_id = t.thread_id)
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
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, COALESCE(sqlc.arg(config_override), '{}'), sqlc.arg(agent_key), sqlc.narg(prompt_version_id), sqlc.arg(pending_launch), sqlc.arg(manually_renamed))
ON CONFLICT (thread_id) DO UPDATE
SET name = excluded.name,
    prompt = excluded.prompt,
    model = excluded.model,
    cwd = excluded.cwd,
    status = excluded.status,
    port = excluded.port,
    pid = excluded.pid,
    updated_at = excluded.updated_at,
    owner_thread_id = excluded.owner_thread_id,
    parent_agent_id = excluded.parent_agent_id,
    agent_type = excluded.agent_type,
    agent_memory_scope = excluded.agent_memory_scope,
    config_override = excluded.config_override,
    agent_key = excluded.agent_key,
    prompt_version_id = excluded.prompt_version_id,
    pending_launch = excluded.pending_launch,
    manually_renamed = excluded.manually_renamed;

-- name: UpdateAgentThreadStatus :exec
UPDATE agent_threads
SET status = ?,
    updated_at = ?
WHERE thread_id = ?;

-- name: DeleteAgentThreadByID :exec
DELETE FROM agent_threads
WHERE thread_id = ?;

-- name: ResetRunningAgentThreads :exec
UPDATE agent_threads
SET status = 'created'
WHERE status = 'running';

-- name: ExpireStaleAgentThreads :execrows
UPDATE agent_threads
SET status = 'expired',
    updated_at = ?
WHERE status IN ('created', 'running')
  AND updated_at < ?;

-- name: AgentThreadRunningExists :one
SELECT EXISTS(
    SELECT 1
    FROM agent_threads
    WHERE thread_id = ? AND status = 'running'
);

-- name: ListAgentThreadCwds :many
SELECT thread_id, cwd
FROM agent_threads
WHERE cwd <> ''
GROUP BY cwd
ORDER BY cwd ASC
LIMIT 100;

-- name: ListAgentThreadCwdsByPrefix :many
SELECT thread_id, cwd
FROM agent_threads
WHERE cwd <> ''
  AND cwd LIKE ? || '%'
GROUP BY cwd
ORDER BY cwd ASC
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
WHERE parent_agent_id = ?;

-- name: AgentThreadExists :one
SELECT EXISTS(
    SELECT 1
    FROM agent_threads
    WHERE thread_id = ?
);

-- name: CountAllThreads :one
SELECT COUNT(*) FROM agent_threads;

-- name: ListAgentThreadConfigsByIDs :many
SELECT thread_id, model, config_override
FROM agent_threads
WHERE thread_id IN (sqlc.slice(thread_ids));
