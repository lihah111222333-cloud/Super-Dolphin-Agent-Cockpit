-- name: LoadAgentThreadPromptSnapshot :one
SELECT prompt_snapshot
FROM agent_threads
WHERE thread_id = ?
LIMIT 1;

-- name: UpdateAgentThreadPromptSnapshot :execrows
UPDATE agent_threads
SET prompt_snapshot = ?
WHERE thread_id = ?;
