-- name: GetAgentProviderBindingByProviderThread :one
SELECT agent_id, provider, provider_thread_id, codex_thread_id, rollout_path, cwd, parent_agent_id, agent_type, agent_memory_scope, archived, created_at, updated_at, session_uuid
FROM agent_provider_binding
WHERE provider = $1 AND provider_thread_id = $2;

-- name: UpsertAgentProviderBinding :exec
INSERT INTO agent_provider_binding (
    agent_id, provider, provider_thread_id, codex_thread_id, rollout_path, cwd, parent_agent_id, agent_type, agent_memory_scope, archived, created_at, updated_at, session_uuid
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, false, $10, $11, $12)
ON CONFLICT (agent_id) DO UPDATE
SET provider = EXCLUDED.provider,
    provider_thread_id = EXCLUDED.provider_thread_id,
    codex_thread_id = EXCLUDED.codex_thread_id,
    rollout_path = EXCLUDED.rollout_path,
    cwd = EXCLUDED.cwd,
    parent_agent_id = EXCLUDED.parent_agent_id,
    agent_type = EXCLUDED.agent_type,
    agent_memory_scope = EXCLUDED.agent_memory_scope,
    session_uuid = EXCLUDED.session_uuid,
    updated_at = EXCLUDED.updated_at;

-- name: DeleteAgentProviderBindingByAgentID :exec
DELETE FROM agent_provider_binding
WHERE agent_id = $1;

-- name: UpdateAgentProviderBindingSessionUUID :exec
UPDATE agent_provider_binding
SET session_uuid = $1,
    updated_at = $2
WHERE agent_id = $3;

-- name: UpdateAgentProviderBindingArchived :exec
UPDATE agent_provider_binding
SET archived = $1,
    updated_at = $2
WHERE agent_id = $3;

-- name: GetAgentProviderBindingByAgentID :one
SELECT agent_id, provider, provider_thread_id, codex_thread_id, rollout_path, cwd, parent_agent_id, agent_type, agent_memory_scope, archived, created_at, updated_at, session_uuid
FROM agent_provider_binding
WHERE agent_id = $1;
