-- name: GetPromptTemplate :one
SELECT id, prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, description, enabled, manually_edited, match_when, priority, created_by, updated_by, created_at, updated_at
FROM prompt_templates
WHERE prompt_key = $1;

-- name: DeletePromptTemplate :execrows
DELETE FROM prompt_templates
WHERE prompt_key = $1;

-- name: InsertPromptVersion :one
INSERT INTO prompt_versions (
    prompt_key, title, agent_key, tool_name, prompt_text,
    variables, tags, description, enabled, created_by, updated_by, source_updated_at
) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10, $11, $12)
RETURNING id;

-- name: UpsertPromptTemplate :one
INSERT INTO prompt_templates (
    prompt_key, title, agent_key, tool_name, prompt_text,
    variables, tags, description, enabled, manually_edited, match_when, priority,
    created_by, updated_by, updated_at
) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10, $11::jsonb, $12, $13, $14, NOW())
ON CONFLICT (prompt_key) DO UPDATE
SET title = EXCLUDED.title,
    agent_key = EXCLUDED.agent_key,
    tool_name = EXCLUDED.tool_name,
    prompt_text = EXCLUDED.prompt_text,
    variables = EXCLUDED.variables,
    tags = EXCLUDED.tags,
    description = EXCLUDED.description,
    enabled = EXCLUDED.enabled,
    manually_edited = EXCLUDED.manually_edited,
    match_when = EXCLUDED.match_when,
    priority = EXCLUDED.priority,
    updated_by = EXCLUDED.updated_by,
    updated_at = NOW()
RETURNING id, prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, description, enabled, manually_edited, match_when, priority, created_by, updated_by, created_at, updated_at;

-- name: ListPromptTemplates :many
SELECT id, prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, description, enabled, manually_edited, match_when, priority, created_by, updated_by, created_at, updated_at
FROM prompt_templates
WHERE (sqlc.arg(agent_key)::text = '' OR agent_key = sqlc.arg(agent_key))
  AND (sqlc.arg(keyword)::text = ''
    OR prompt_key ILIKE '%' || sqlc.arg(keyword) || '%'
    OR title ILIKE '%' || sqlc.arg(keyword) || '%'
    OR prompt_text ILIKE '%' || sqlc.arg(keyword) || '%')
  AND (sqlc.arg(cwd)::text = ''
    OR NOT EXISTS (
      SELECT 1
      FROM jsonb_array_elements_text(tags) AS tag(value)
      WHERE tag.value LIKE 'scope.cwd:%'
    )
    OR tags ? ('scope.cwd:' || sqlc.arg(cwd)::text))
ORDER BY updated_at DESC
LIMIT sqlc.arg(limit_count);
