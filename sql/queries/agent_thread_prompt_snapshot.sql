-- name: LoadAgentThreadPromptSnapshot :one
SELECT prompt_snapshot
FROM agent_threads
WHERE thread_id = $1
LIMIT 1;

-- name: UpdateAgentThreadPromptSnapshot :execrows
UPDATE agent_threads
SET prompt_snapshot = $2
WHERE thread_id = $1;
