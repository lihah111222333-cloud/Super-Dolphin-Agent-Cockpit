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
WHERE agent_id = ?;

-- name: ListAgentThreadBindings :many
SELECT agent_id, provider, provider_thread_id, codex_thread_id, rollout_path, cwd, parent_agent_id, agent_type, agent_memory_scope, archived, created_at, updated_at, session_uuid, codex_home, codex_instance_key, codex_model_provider, provider_recovery_home
FROM agent_provider_binding
ORDER BY created_at DESC, agent_id DESC;

-- name: GetThreadByAgent :one
SELECT COALESCE(NULLIF(codex_thread_id, ''), provider_thread_id) AS thread_id
FROM agent_provider_binding
WHERE agent_id = ?;

-- name: UpdateAgentCwd :exec
UPDATE agent_provider_binding
SET cwd = ?,
    updated_at = ?
WHERE agent_id = ?;

-- name: RebindAgentThreadTx :exec
-- SQLite does not support DML inside CTEs; Task rewrite: DELETE then INSERT.
-- The Go layer wraps these in a transaction. This placeholder keeps the
-- same query name so generated code compiles; real atomicity is in the Go tx.
INSERT INTO agent_provider_binding (
    agent_id, provider, provider_thread_id, codex_thread_id, rollout_path, cwd,
    parent_agent_id, agent_type, agent_memory_scope, archived, created_at, updated_at, session_uuid
)
VALUES (
    sqlc.arg(agent_id), 'codex', sqlc.arg(thread_id), sqlc.arg(thread_id), '', sqlc.arg(cwd),
    '', '', '', false, sqlc.arg(created_at), sqlc.arg(updated_at), ''
)
ON CONFLICT (agent_id) DO UPDATE
SET provider_thread_id = EXCLUDED.provider_thread_id,
    codex_thread_id    = EXCLUDED.codex_thread_id,
    cwd                = EXCLUDED.cwd,
    updated_at         = EXCLUDED.updated_at;
