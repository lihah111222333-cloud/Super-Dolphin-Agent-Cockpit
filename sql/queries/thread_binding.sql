-- name: BindAgentThread :exec
INSERT INTO agent_provider_binding (
    agent_id,
    provider,
    provider_thread_id,
    codex_thread_id,
    rollout_path,
    cwd,
    archived,
    created_at,
    updated_at,
    session_uuid
) VALUES (
    $1,
    'codex',
    $2,
    $2,
    '',
    $3,
    false,
    $4,
    $5,
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
SELECT agent_id, provider, provider_thread_id, codex_thread_id, rollout_path, cwd, archived, created_at, updated_at, session_uuid
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
