-- name: InsertAgentFeedbackEvent :one
INSERT INTO agent_feedback_events (
    thread_id,
    turn_id,
    agent_key,
    prompt_version_id,
    event_type,
    actor,
    payload
) VALUES (
    ?, ?, ?, sqlc.narg(prompt_version_id), ?, ?, COALESCE(sqlc.arg(payload), '{}')
)
RETURNING id, thread_id, turn_id, agent_key, prompt_version_id, event_type, actor, CAST(payload AS BLOB) AS payload, created_at;

-- name: ListAgentFeedbackEventsByThread :many
SELECT id, thread_id, turn_id, agent_key, prompt_version_id, event_type, actor, CAST(payload AS BLOB) AS payload, created_at
FROM agent_feedback_events
WHERE thread_id = ?
ORDER BY created_at DESC, id DESC
LIMIT ?;

-- name: ListAgentFeedbackEventsByAgent :many
SELECT id, thread_id, turn_id, agent_key, prompt_version_id, event_type, actor, CAST(payload AS BLOB) AS payload, created_at
FROM agent_feedback_events
WHERE agent_key = ?
ORDER BY created_at DESC, id DESC
LIMIT ?;
