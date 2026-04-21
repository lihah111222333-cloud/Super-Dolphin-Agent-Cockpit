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
    $1, $2, $3, sqlc.narg(prompt_version_id), $4, $5, COALESCE(sqlc.arg(payload), '{}'::jsonb)
)
RETURNING id, thread_id, turn_id, agent_key, prompt_version_id, event_type, actor, payload, created_at;

-- name: ListAgentFeedbackEventsByThread :many
SELECT id, thread_id, turn_id, agent_key, prompt_version_id, event_type, actor, payload, created_at
FROM agent_feedback_events
WHERE thread_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: ListAgentFeedbackEventsByAgent :many
SELECT id, thread_id, turn_id, agent_key, prompt_version_id, event_type, actor, payload, created_at
FROM agent_feedback_events
WHERE agent_key = $1
ORDER BY created_at DESC, id DESC
LIMIT $2;
