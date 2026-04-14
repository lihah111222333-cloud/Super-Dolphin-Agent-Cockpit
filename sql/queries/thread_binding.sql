-- name: BindAgentThread :exec
INSERT INTO agent_provider_binding (
    agent_id,
    provider,
    provider_thread_id,
    codex_thread_id,
    rollout_path,
    cwd,
    parent_agent_id,
    agent_type,
    agent_memory_scope,
    archived,
    created_at,
    updated_at,
    session_uuid
) VALUES (
    sqlc.arg(agent_id),
    'codex',
    sqlc.arg(thread_id),
    sqlc.arg(thread_id),
    '',
    sqlc.arg(cwd),
    '',
    '',
    '',
    false,
    sqlc.arg(created_at),
    sqlc.arg(updated_at),
    ''
)
ON CONFLICT (agent_id) DO UPDATE
SET codex_thread_id = EXCLUDED.codex_thread_id,
    cwd = EXCLUDED.cwd,
    updated_at = EXCLUDED.updated_at;

-- name: UnbindAgentThread :exec
DELETE FROM agent_provider_binding
WHERE agent_id = $1;

-- name: ListAgentThreadBindings :many
SELECT agent_id, provider, provider_thread_id, codex_thread_id, rollout_path, cwd, parent_agent_id, agent_type, agent_memory_scope, archived, created_at, updated_at, session_uuid
FROM agent_provider_binding
ORDER BY created_at DESC, agent_id DESC;

-- name: GetThreadByAgent :one
SELECT COALESCE(NULLIF(codex_thread_id, ''), provider_thread_id) AS thread_id
FROM agent_provider_binding
WHERE agent_id = $1;

-- name: UpdateAgentCwd :exec
UPDATE agent_provider_binding
SET cwd = $1,
    updated_at = $2
WHERE agent_id = $3;
