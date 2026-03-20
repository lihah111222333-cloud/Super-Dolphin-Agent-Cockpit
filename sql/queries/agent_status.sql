-- name: UpsertAgentStatus :one
INSERT INTO agent_status (agent_id, agent_name, session_id, status, stagnant_sec, error, output_tail, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, NOW(), NOW())
ON CONFLICT (agent_id) DO UPDATE
SET agent_name = EXCLUDED.agent_name,
    session_id = EXCLUDED.session_id,
    status = EXCLUDED.status,
    stagnant_sec = EXCLUDED.stagnant_sec,
    error = EXCLUDED.error,
    output_tail = EXCLUDED.output_tail,
    updated_at = NOW()
RETURNING agent_id, agent_name, session_id, status, stagnant_sec, error, output_tail, created_at, updated_at;

-- name: GetAgentStatus :one
SELECT agent_id, agent_name, session_id, status, stagnant_sec, error, output_tail, created_at, updated_at
FROM agent_status
WHERE agent_id = $1;

-- name: ListAgentStatuses :many
SELECT agent_id, agent_name, session_id, status, stagnant_sec, error, output_tail, created_at, updated_at
FROM agent_status
WHERE ($1::text = '' OR status = $1)
ORDER BY updated_at DESC
LIMIT 500;
