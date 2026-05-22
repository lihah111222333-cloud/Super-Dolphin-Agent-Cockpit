-- name: GetPromptTemplate :one
SELECT id, prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, description, when_to_use, enabled, manually_edited, match_when, priority, created_by, updated_by, created_at, updated_at
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
    variables, tags, description, when_to_use, enabled, manually_edited, match_when, priority,
    created_by, updated_by, updated_at
) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10, $11, $12::jsonb, $13, $14, $15, NOW())
ON CONFLICT (prompt_key) DO UPDATE
SET title = EXCLUDED.title,
    agent_key = EXCLUDED.agent_key,
    tool_name = EXCLUDED.tool_name,
    prompt_text = EXCLUDED.prompt_text,
    variables = EXCLUDED.variables,
    tags = EXCLUDED.tags,
    description = EXCLUDED.description,
    when_to_use = EXCLUDED.when_to_use,
    enabled = EXCLUDED.enabled,
    manually_edited = EXCLUDED.manually_edited,
    match_when = EXCLUDED.match_when,
    priority = EXCLUDED.priority,
    updated_by = EXCLUDED.updated_by,
    updated_at = NOW()
RETURNING id, prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, description, when_to_use, enabled, manually_edited, match_when, priority, created_by, updated_by, created_at, updated_at;

-- name: ListPromptTemplates :many
SELECT id, prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, description, when_to_use, enabled, manually_edited, match_when, priority, created_by, updated_by, created_at, updated_at
FROM prompt_templates
WHERE ($1::text = '' OR agent_key = $1)
  AND ($2::text = ''
    OR prompt_key ILIKE '%' || $2 || '%'
    OR title ILIKE '%' || $2 || '%'
    OR prompt_text ILIKE '%' || $2 || '%')
  AND (NOT sqlc.arg(runtime_visible)::bool
    OR (
      enabled = TRUE
      AND (tags ? ('scope.cwd:' || sqlc.arg(cwd)::text) OR tags ? 'scope.global')
    ))
ORDER BY updated_at DESC
LIMIT sqlc.arg(limit_count);

-- name: GetPromptRecallSectionBody :one
SELECT s.body
FROM prompt_template_sections s
JOIN prompt_templates t ON t.id = s.template_id
WHERE s.recall_topic = $1
  AND s.trigger_type = 'recall'
  AND s.enabled = TRUE
  AND t.enabled = TRUE
  AND (t.tags ? ('scope.cwd:' || sqlc.arg(cwd)::text) OR t.tags ? 'scope.global')
ORDER BY CASE
    WHEN t.tags ? ('scope.cwd:' || sqlc.arg(cwd)::text) THEN 0
    WHEN t.tags ? 'scope.global' THEN 1
    ELSE 2
  END,
  s.ordinal,
  s.id
LIMIT 1;

-- name: ListPromptTemplateSectionsByTemplate :many
SELECT s.id, s.template_id, s.section_key, s.region, s.ordinal, s.body,
       s.trigger_type, s.recall_topic, s.enabled
FROM prompt_template_sections s
WHERE s.template_id = $1
  AND s.enabled = TRUE
  AND s.trigger_type <> 'recall'
ORDER BY CASE s.region WHEN 'static' THEN 0 ELSE 1 END, s.ordinal, s.id;
