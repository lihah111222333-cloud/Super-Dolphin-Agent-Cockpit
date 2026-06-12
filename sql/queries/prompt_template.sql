-- name: GetPromptTemplate :one
SELECT id, prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, description, when_to_use, enabled, manually_edited, match_when, priority, created_by, updated_by, created_at, updated_at
FROM prompt_templates
WHERE prompt_key = ?;

-- name: DeletePromptTemplate :execrows
DELETE FROM prompt_templates
WHERE prompt_key = ?;

-- name: InsertPromptVersion :one
INSERT INTO prompt_versions (
    prompt_key, title, agent_key, tool_name, prompt_text,
    variables, tags, description, enabled, created_by, updated_by, source_updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: UpsertPromptTemplate :one
INSERT INTO prompt_templates (
    prompt_key, title, agent_key, tool_name, prompt_text,
    variables, tags, description, when_to_use, enabled, manually_edited, match_when, priority,
    created_by, updated_by, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000))
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
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
RETURNING id, prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, description, when_to_use, enabled, manually_edited, match_when, priority, created_by, updated_by, created_at, updated_at;

-- name: CreatePromptTemplate :one
INSERT INTO prompt_templates (
    prompt_key, title, agent_key, tool_name, prompt_text,
    variables, tags, description, when_to_use, enabled, manually_edited, match_when, priority,
    created_by, updated_by, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000))
ON CONFLICT (prompt_key) DO NOTHING
RETURNING id, prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, description, when_to_use, enabled, manually_edited, match_when, priority, created_by, updated_by, created_at, updated_at;

-- name: ListPromptTemplates :many
SELECT id, prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, description, when_to_use, enabled, manually_edited, match_when, priority, created_by, updated_by, created_at, updated_at
FROM prompt_templates
WHERE (sqlc.arg(agent_key) = '' OR agent_key = sqlc.arg(agent_key))
  AND (sqlc.arg(keyword) = ''
    OR prompt_key LIKE '%' || sqlc.arg(keyword) || '%'
    OR title LIKE '%' || sqlc.arg(keyword) || '%'
    OR prompt_text LIKE '%' || sqlc.arg(keyword) || '%')
  AND (instr(tags, '"scope.cwd:') = 0
    OR instr(tags, '"scope.global"') > 0
    OR instr(tags, '"scope.cwd:' || sqlc.arg(cwd) || '"') > 0)
ORDER BY updated_at DESC
LIMIT sqlc.arg(limit_count);
