-- name: ListEnabledPromptRoutingTests :many
SELECT id, input, expected_prompt_key, note, enabled, created_at, updated_at
FROM prompt_routing_tests
WHERE enabled = 1
ORDER BY id;
