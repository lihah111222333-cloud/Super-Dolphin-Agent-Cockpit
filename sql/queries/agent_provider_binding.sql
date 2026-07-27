-- name: GetAgentProviderBindingByProviderThread :one
SELECT agent_id, provider, provider_thread_id, codex_thread_id, rollout_path, cwd, parent_agent_id, agent_type, agent_memory_scope, archived, created_at, updated_at, session_uuid, codex_home, codex_instance_key, codex_model_provider
FROM agent_provider_binding
WHERE provider = ? AND provider_thread_id = ?;

-- name: UpsertAgentProviderBinding :exec
-- Codex identity columns use "'' preserves existing value" semantics so
-- non-P1a callers that pass '' do not overwrite an already-persisted
-- identity. Codex tuple fields are immutable once non-empty; non-empty
-- codex_home repair is only for caller-validated aliases on the same tuple.
INSERT INTO agent_provider_binding (
    agent_id, provider, provider_thread_id, codex_thread_id, rollout_path, cwd, parent_agent_id, agent_type, agent_memory_scope, archived, created_at, updated_at, session_uuid, codex_home, codex_instance_key, codex_model_provider
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, false, ?, ?, ?, ?, ?, ?)
ON CONFLICT (agent_id) DO UPDATE
SET provider = EXCLUDED.provider,
    provider_thread_id = CASE WHEN EXCLUDED.provider_thread_id = '' THEN agent_provider_binding.provider_thread_id ELSE EXCLUDED.provider_thread_id END,
    codex_thread_id = EXCLUDED.codex_thread_id,
    rollout_path = EXCLUDED.rollout_path,
    cwd = EXCLUDED.cwd,
    parent_agent_id = EXCLUDED.parent_agent_id,
    agent_type = EXCLUDED.agent_type,
    agent_memory_scope = EXCLUDED.agent_memory_scope,
    session_uuid = CASE WHEN EXCLUDED.session_uuid = '' THEN agent_provider_binding.session_uuid ELSE EXCLUDED.session_uuid END,
    codex_home = CASE WHEN EXCLUDED.codex_home = '' THEN agent_provider_binding.codex_home ELSE EXCLUDED.codex_home END,
    codex_instance_key = CASE WHEN EXCLUDED.codex_instance_key = '' THEN agent_provider_binding.codex_instance_key ELSE EXCLUDED.codex_instance_key END,
    codex_model_provider = CASE WHEN EXCLUDED.codex_model_provider = '' THEN agent_provider_binding.codex_model_provider ELSE EXCLUDED.codex_model_provider END,
    updated_at = EXCLUDED.updated_at;

-- name: DeleteAgentProviderBindingByAgentID :exec
DELETE FROM agent_provider_binding
WHERE agent_id = ?;

-- name: UpdateAgentProviderBindingSessionUUID :exec
UPDATE agent_provider_binding
SET session_uuid = ?,
    provider_thread_id = CASE
        WHEN provider_thread_id = '' OR provider_thread_id = agent_id
        THEN ?
        ELSE provider_thread_id
    END,
    updated_at = ?
WHERE agent_id = ?;

-- name: UpdateAgentProviderBindingArchived :execrows
UPDATE agent_provider_binding
SET archived = ?,
    updated_at = ?
WHERE agent_id = ?;

-- name: UpdateAgentProviderBindingProviderThreadID :exec
UPDATE agent_provider_binding
SET provider_thread_id = ?,
    updated_at = ?
WHERE agent_id = ?;

-- name: GetAgentProviderBindingByAgentID :one
SELECT agent_id, provider, provider_thread_id, codex_thread_id, rollout_path, cwd, parent_agent_id, agent_type, agent_memory_scope, archived, created_at, updated_at, session_uuid, codex_home, codex_instance_key, codex_model_provider
FROM agent_provider_binding
WHERE agent_id = ?;
