-- name: GetAgentProviderBindingByProviderThread :one
SELECT agent_id, provider, provider_thread_id, codex_thread_id, rollout_path, cwd, archived, created_at, updated_at, session_uuid
FROM agent_provider_binding
WHERE provider = $1 AND provider_thread_id = $2;

-- name: UpsertAgentProviderBinding :exec
INSERT INTO agent_provider_binding (
    agent_id, provider, provider_thread_id, codex_thread_id, rollout_path, cwd, archived, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, false, $7, $8)
ON CONFLICT (agent_id) DO UPDATE
SET provider = EXCLUDED.provider,
    provider_thread_id = EXCLUDED.provider_thread_id,
    codex_thread_id = EXCLUDED.codex_thread_id,
    rollout_path = EXCLUDED.rollout_path,
    cwd = EXCLUDED.cwd,
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
SELECT agent_id, provider, provider_thread_id, codex_thread_id, rollout_path, cwd, archived, created_at, updated_at, session_uuid
FROM agent_provider_binding
WHERE agent_id = $1;
