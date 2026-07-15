-- name: GetPromptTemplate :one
SELECT id, prompt_key, title, agent_key, tool_name, prompt_text,
       CAST(variables AS BLOB) AS variables, CAST(tags AS BLOB) AS tags,
       description, when_to_use, enabled, manually_edited,
       CAST(match_when AS BLOB) AS match_when, priority, created_by, updated_by, created_at, updated_at
FROM prompt_templates
WHERE prompt_key = :prompt_key;

-- name: DeletePromptTemplate :execrows
DELETE FROM prompt_templates
WHERE prompt_key = :prompt_key;

-- name: InsertPromptVersion :execresult
INSERT INTO prompt_versions (
    prompt_key, title, agent_key, tool_name, prompt_text,
    variables, tags, description, enabled, created_by, updated_by, source_updated_at, created_at, archived_at
) VALUES (
    :prompt_key, :title, :agent_key, :tool_name, :prompt_text,
    :variables, :tags, :description, :enabled, :created_by, :updated_by, :source_updated_at,
    (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000)
);

-- name: InsertPromptTemplate :execrows
INSERT INTO prompt_templates (
    prompt_key, title, agent_key, tool_name, prompt_text,
    variables, tags, description, when_to_use, enabled, manually_edited, match_when, priority,
    created_by, updated_by, created_at, updated_at
) VALUES (
    :prompt_key, :title, :agent_key, :tool_name, :prompt_text,
    :variables, :tags, :description, :when_to_use, :enabled, :manually_edited, :match_when, :priority,
    :created_by, :updated_by, (CAST(strftime('%s','now') AS INTEGER) * 1000),
    (CAST(strftime('%s','now') AS INTEGER) * 1000)
);

-- name: UpdatePromptTemplate :execrows
UPDATE prompt_templates
SET title = :title,
    agent_key = :agent_key,
    tool_name = :tool_name,
    prompt_text = :prompt_text,
    variables = :variables,
    tags = :tags,
    description = :description,
    when_to_use = :when_to_use,
    enabled = :enabled,
    manually_edited = :manually_edited,
    match_when = :match_when,
    priority = :priority,
    updated_by = :updated_by,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE prompt_key = :prompt_key;

-- name: ListPromptTemplates :many
SELECT id, prompt_key, title, agent_key, tool_name, prompt_text,
       CAST(variables AS BLOB) AS variables, CAST(tags AS BLOB) AS tags,
       description, when_to_use, enabled, manually_edited,
       CAST(match_when AS BLOB) AS match_when, priority, created_by, updated_by, created_at, updated_at
FROM prompt_templates
WHERE (:agent_key = '' OR agent_key = :agent_key)
  AND (:keyword = ''
    OR prompt_key LIKE :keyword
    OR title LIKE :keyword
    OR prompt_text LIKE :keyword)
  AND (
    :runtime_visible = 0
    OR (
      enabled = 1
      AND (
        instr(CAST(tags AS TEXT), json_quote('scope.global')) > 0
        OR instr(CAST(tags AS TEXT), json_quote(:scope_cwd)) > 0
      )
    )
  )
ORDER BY updated_at DESC
LIMIT :limit_count;

-- name: GetPromptRecallSectionBody :one
WITH ranked_prompt_sections AS (
  SELECT s.body, s.ordinal, s.id,
         instr(CAST(t.tags AS TEXT), json_quote(:scope_rank)) AS cwd_rank
  FROM prompt_template_sections s
  JOIN prompt_templates t ON t.id = s.template_id
  WHERE s.recall_topic = :recall_topic
    AND s.trigger_type = 'recall'
    AND s.enabled = 1
    AND t.enabled = 1
    AND (
      instr(CAST(t.tags AS TEXT), json_quote('scope.global')) > 0
      OR instr(CAST(t.tags AS TEXT), json_quote(:scope_cwd)) > 0
    )
)
SELECT body
FROM ranked_prompt_sections
ORDER BY cwd_rank DESC, ordinal, id
LIMIT 1;

-- name: ListPromptTemplateSectionsByTemplate :many
SELECT s.id, s.template_id, s.section_key, s.region, s.ordinal, s.body,
       s.trigger_type, s.recall_topic, s.enabled
FROM prompt_template_sections s
WHERE s.template_id = :template_id
  AND s.enabled = 1
  AND s.trigger_type <> 'recall'
ORDER BY (s.region = 'static') DESC, s.ordinal, s.id;
