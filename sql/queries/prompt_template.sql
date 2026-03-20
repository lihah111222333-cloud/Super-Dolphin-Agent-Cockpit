-- name: GetPromptTemplate :one
SELECT id, prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, description, enabled, created_by, updated_by, created_at, updated_at
FROM prompt_templates
WHERE prompt_key = $1;

-- name: InsertPromptVersion :exec
INSERT INTO prompt_versions (
    prompt_key, title, agent_key, tool_name, prompt_text,
    variables, tags, enabled, created_by, updated_by, source_updated_at
) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10, $11);

-- name: UpsertPromptTemplate :one
INSERT INTO prompt_templates (
    prompt_key, title, agent_key, tool_name, prompt_text,
    variables, tags, description, enabled, created_by, updated_by, updated_at
) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10, $11, NOW())
ON CONFLICT (prompt_key) DO UPDATE
SET title = EXCLUDED.title,
    agent_key = EXCLUDED.agent_key,
    tool_name = EXCLUDED.tool_name,
    prompt_text = EXCLUDED.prompt_text,
    variables = EXCLUDED.variables,
    tags = EXCLUDED.tags,
    description = EXCLUDED.description,
    enabled = EXCLUDED.enabled,
    updated_by = EXCLUDED.updated_by,
    updated_at = NOW()
RETURNING id, prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, description, enabled, created_by, updated_by, created_at, updated_at;

-- name: ListPromptTemplates :many
SELECT id, prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, description, enabled, created_by, updated_by, created_at, updated_at
FROM prompt_templates
WHERE ($1::text = '' OR agent_key = $1)
  AND ($2::text = ''
    OR prompt_key ILIKE '%' || $2 || '%'
    OR title ILIKE '%' || $2 || '%'
    OR prompt_text ILIKE '%' || $2 || '%')
ORDER BY updated_at DESC
LIMIT $3;
