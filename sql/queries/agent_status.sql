-- name: UpsertAgentStatus :one
INSERT INTO agent_status (agent_id, agent_name, session_id, status, stagnant_sec, error, output_tail, created_at, updated_at)
VALUES (
    sqlc.arg(agent_id),
    sqlc.arg(agent_name),
    sqlc.arg(session_id),
    sqlc.arg(status),
    sqlc.arg(stagnant_sec),
    sqlc.arg(error),
    sqlc.arg(output_tail),
    sqlc.arg(now),
    sqlc.arg(now)
)
ON CONFLICT (agent_id) DO UPDATE
SET agent_name = EXCLUDED.agent_name,
    session_id = EXCLUDED.session_id,
    status = EXCLUDED.status,
    stagnant_sec = EXCLUDED.stagnant_sec,
    error = EXCLUDED.error,
    output_tail = EXCLUDED.output_tail,
    updated_at = EXCLUDED.updated_at
RETURNING agent_id, agent_name, session_id, status, stagnant_sec, error, output_tail, created_at, updated_at;

-- name: GetAgentStatus :one
SELECT agent_id, agent_name, session_id, status, stagnant_sec, error, output_tail, created_at, updated_at
FROM agent_status
WHERE agent_id = ?;

-- name: ListAgentStatuses :many
SELECT agent_id, agent_name, session_id, status, stagnant_sec, error, output_tail, created_at, updated_at
FROM agent_status
WHERE (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
ORDER BY updated_at DESC
LIMIT 500;
