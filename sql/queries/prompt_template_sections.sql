-- name: ListPromptTemplateSectionsByTemplate :many
SELECT id, template_id, section_key, region, ordinal, body, enable_when, enabled,
       created_at, updated_at, trigger_type, recall_topic
FROM prompt_template_sections
WHERE template_id = ?
ORDER BY region, ordinal, id;

-- name: ListPromptTemplateSectionsByTemplates :many
SELECT id, template_id, section_key, region, ordinal, body, enable_when, enabled,
       created_at, updated_at, trigger_type, recall_topic
FROM prompt_template_sections
WHERE template_id IN (sqlc.slice(template_ids))
ORDER BY template_id, region, ordinal, id;

-- name: ListRecallSections :many
WITH scoped AS (
  SELECT s.id, s.template_id, s.section_key, s.region, s.ordinal,
         s.enable_when, s.enabled, s.created_at, s.updated_at,
         s.trigger_type, s.recall_topic,
         t.prompt_key AS template_prompt_key,
         t.title AS template_title,
         t.description AS template_description,
         t.when_to_use AS template_when_to_use,
         t.tags AS template_tags,
         CASE
           WHEN instr(t.tags, '"scope.cwd:' || sqlc.arg(cwd) || '"') > 0 THEN 0
           WHEN instr(t.tags, '"scope.global"') > 0 THEN 1
           ELSE 2
         END AS scope_rank
  FROM prompt_template_sections s
  JOIN prompt_templates t ON t.id = s.template_id
  WHERE s.trigger_type = 'recall'
    AND s.enabled = TRUE
    AND t.enabled = TRUE
    AND TRIM(s.recall_topic) <> ''
    AND sqlc.arg(cwd) <> ''
    AND (instr(t.tags, '"scope.cwd:' || sqlc.arg(cwd) || '"') > 0 OR instr(t.tags, '"scope.global"') > 0)
),
ranked AS (
  SELECT scoped.*,
         ROW_NUMBER() OVER (
           PARTITION BY TRIM(recall_topic)
           ORDER BY scope_rank ASC, id ASC
         ) AS scope_row
  FROM scoped
)
SELECT id, template_id, section_key, region, ordinal,
       enable_when, enabled, created_at, updated_at,
       trigger_type, recall_topic,
       template_prompt_key, template_title, template_description,
       template_when_to_use, template_tags
FROM ranked
WHERE scope_row = 1
ORDER BY recall_topic, id;

-- name: ListDefaultRuleSections :many
WITH scoped AS (
  SELECT s.id, s.template_id, s.section_key, s.region, s.ordinal, s.body,
         s.enable_when, s.enabled, s.created_at, s.updated_at,
         s.trigger_type, s.recall_topic,
         t.prompt_key AS template_prompt_key,
         t.title AS template_title,
         t.tags AS template_tags,
         t.priority,
         CASE
           WHEN instr(t.tags, '"scope.cwd:' || sqlc.arg(cwd) || '"') > 0 THEN 0
           WHEN instr(t.tags, '"scope.global"') > 0 THEN 1
           ELSE 2
         END AS scope_rank,
         CASE
           WHEN TRIM(t.title) <> '' THEN
             LOWER(TRIM(t.title)) || char(31) || LOWER(TRIM(COALESCE(NULLIF(s.section_key, ''), s.body)))
           ELSE
             LOWER(TRIM(COALESCE(NULLIF(s.section_key, ''), NULLIF(t.prompt_key, ''), s.body)))
         END AS rule_identity
  FROM prompt_template_sections s
  JOIN prompt_templates t ON t.id = s.template_id
  WHERE t.agent_key = 'default_rule'
    AND s.trigger_type = 'always'
    AND s.enabled = TRUE
    AND t.enabled = TRUE
    AND TRIM(s.body) <> ''
    AND sqlc.arg(cwd) <> ''
    AND (instr(t.tags, '"scope.cwd:' || sqlc.arg(cwd) || '"') > 0 OR instr(t.tags, '"scope.global"') > 0)
),
ranked AS (
  SELECT scoped.*,
         ROW_NUMBER() OVER (
           PARTITION BY rule_identity
           ORDER BY scope_rank ASC, id ASC
         ) AS scope_row
  FROM scoped
)
SELECT id, template_id, section_key, region, ordinal, body,
       enable_when, enabled, created_at, updated_at,
       trigger_type, recall_topic, template_prompt_key,
       template_title, template_tags
FROM ranked
WHERE scope_row = 1
ORDER BY priority DESC, template_prompt_key, ordinal, id;

-- name: LockRecallTopicInCWD :exec
INSERT INTO prompt_recall_topics (cwd, topic, template_id, section_key)
VALUES (?, ?, 0, '')
ON CONFLICT (cwd, topic) DO UPDATE SET
    cwd = EXCLUDED.cwd,
    topic = EXCLUDED.topic;

-- name: UpsertPromptRecallTopicTargetInCWD :exec
INSERT INTO prompt_recall_topics (cwd, topic, template_id, section_key)
VALUES (?, ?, ?, ?)
ON CONFLICT (cwd, topic) DO UPDATE SET
    template_id = EXCLUDED.template_id,
    section_key = EXCLUDED.section_key;

-- name: UpsertPromptTemplateSection :one
-- Upsert by (template_id, section_key). Touches updated_at on conflict so
-- operators see when they last edited a row. Empty enable_when stays as-is
-- (NULL or '{}' both mean "always inject" per EvaluateEnableWhen).
INSERT INTO prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled, trigger_type, recall_topic, created_at, updated_at)
VALUES
    (?, ?, ?, ?, ?, ?, ?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000))
ON CONFLICT (template_id, section_key) DO UPDATE SET
    region       = EXCLUDED.region,
    ordinal      = EXCLUDED.ordinal,
    body         = EXCLUDED.body,
    enable_when  = EXCLUDED.enable_when,
    enabled      = EXCLUDED.enabled,
    trigger_type = EXCLUDED.trigger_type,
    recall_topic = EXCLUDED.recall_topic,
    updated_at   = (CAST(strftime('%s','now') AS INTEGER) * 1000)
RETURNING id, template_id, section_key, region, ordinal, body, enable_when, enabled,
          created_at, updated_at, trigger_type, recall_topic;

-- name: DeletePromptTemplateSection :execrows
DELETE FROM prompt_template_sections
WHERE template_id = ? AND section_key = ?;
