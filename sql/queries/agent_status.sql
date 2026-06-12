-- name: UpsertAgentStatus :one
INSERT INTO agent_status (agent_id, agent_name, session_id, status, stagnant_sec, error, output_tail, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000))
ON CONFLICT (agent_id) DO UPDATE
SET agent_name = EXCLUDED.agent_name,
    session_id = EXCLUDED.session_id,
    status = EXCLUDED.status,
    stagnant_sec = EXCLUDED.stagnant_sec,
    error = EXCLUDED.error,
    output_tail = EXCLUDED.output_tail,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
RETURNING agent_id, agent_name, session_id, status, stagnant_sec, error, output_tail, created_at, updated_at;

-- name: GetAgentStatus :one
SELECT agent_id, agent_name, session_id, status, stagnant_sec, error, output_tail, created_at, updated_at
FROM agent_status
WHERE agent_id = ?;

-- name: ListAgentStatuses :many
SELECT agent_id, agent_name, session_id, status, stagnant_sec, error, output_tail, created_at, updated_at
FROM agent_status
WHERE (? = '' OR status = ?)
ORDER BY updated_at DESC
LIMIT 500;
