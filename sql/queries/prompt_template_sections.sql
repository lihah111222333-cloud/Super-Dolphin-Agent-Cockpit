-- name: ListPromptTemplateSectionsByTemplate :many
SELECT id, template_id, section_key, region, ordinal, body, enable_when, enabled,
       created_at, updated_at, trigger_type, recall_topic
FROM prompt_template_sections
WHERE template_id = $1
ORDER BY region, ordinal, id;

-- name: ListPromptTemplateSectionsByTemplates :many
SELECT id, template_id, section_key, region, ordinal, body, enable_when, enabled,
       created_at, updated_at, trigger_type, recall_topic
FROM prompt_template_sections
WHERE template_id = ANY(sqlc.arg(template_ids)::bigint[])
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
           WHEN t.tags ? ('scope.cwd:' || sqlc.arg(cwd)::text) THEN 0
           WHEN t.tags ? 'scope.global' THEN 1
           ELSE 2
         END AS scope_rank
  FROM prompt_template_sections s
  JOIN prompt_templates t ON t.id = s.template_id
  WHERE s.trigger_type = 'recall'
    AND s.enabled = TRUE
    AND t.enabled = TRUE
    AND BTRIM(s.recall_topic) <> ''
    AND sqlc.arg(cwd)::text <> ''
    AND (t.tags ? ('scope.cwd:' || sqlc.arg(cwd)::text) OR t.tags ? 'scope.global')
),
picked AS (
  SELECT DISTINCT ON (BTRIM(recall_topic))
         id, template_id, section_key, region, ordinal,
         enable_when, enabled, created_at, updated_at,
         trigger_type, recall_topic,
         template_prompt_key, template_title, template_description,
         template_when_to_use, template_tags
  FROM scoped
  ORDER BY BTRIM(recall_topic), scope_rank, ordinal, id
)
SELECT id, template_id, section_key, region, ordinal,
       enable_when, enabled, created_at, updated_at,
       trigger_type, recall_topic,
       template_prompt_key, template_title, template_description,
       template_when_to_use, template_tags
FROM picked
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
           WHEN t.tags ? ('scope.cwd:' || sqlc.arg(cwd)::text) THEN 0
           WHEN t.tags ? 'scope.global' THEN 1
           ELSE 2
         END AS scope_rank,
         CASE
           WHEN BTRIM(t.title) <> '' THEN
             LOWER(BTRIM(t.title)) || CHR(31) || LOWER(BTRIM(COALESCE(NULLIF(s.section_key, ''), s.body)))
           ELSE
             LOWER(BTRIM(COALESCE(NULLIF(s.section_key, ''), NULLIF(t.prompt_key, ''), s.body)))
         END AS rule_identity
  FROM prompt_template_sections s
  JOIN prompt_templates t ON t.id = s.template_id
  WHERE t.agent_key = 'default_rule'
    AND s.trigger_type = 'always'
    AND s.enabled = TRUE
    AND t.enabled = TRUE
    AND BTRIM(s.body) <> ''
    AND sqlc.arg(cwd)::text <> ''
    AND (t.tags ? ('scope.cwd:' || sqlc.arg(cwd)::text) OR t.tags ? 'scope.global')
),
picked AS (
  SELECT DISTINCT ON (rule_identity)
         id, template_id, section_key, region, ordinal, body,
         enable_when, enabled, created_at, updated_at,
         trigger_type, recall_topic, template_prompt_key,
         template_title, template_tags, priority, scope_rank
  FROM scoped
  ORDER BY rule_identity, scope_rank, priority DESC, template_prompt_key, ordinal, id
)
SELECT id, template_id, section_key, region, ordinal, body,
       enable_when, enabled, created_at, updated_at,
       trigger_type, recall_topic,
       template_prompt_key, template_title, template_tags
FROM picked
ORDER BY priority DESC, template_prompt_key, ordinal, id;

-- name: LockRecallTopicInCWD :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(cwd)::text || E'\n' || sqlc.arg(topic)::text, 101));

-- name: UpsertPromptTemplateSection :one
-- Upsert by (template_id, section_key). Touches updated_at on conflict so
-- operators see when they last edited a row. Empty enable_when stays as-is
-- (NULL or '{}' both mean "always inject" per EvaluateEnableWhen).
INSERT INTO prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled, trigger_type, recall_topic)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (template_id, section_key) DO UPDATE SET
    region       = EXCLUDED.region,
    ordinal      = EXCLUDED.ordinal,
    body         = EXCLUDED.body,
    enable_when  = EXCLUDED.enable_when,
    enabled      = EXCLUDED.enabled,
    trigger_type = EXCLUDED.trigger_type,
    recall_topic = EXCLUDED.recall_topic,
    updated_at   = NOW()
RETURNING id, template_id, section_key, region, ordinal, body, enable_when, enabled,
          created_at, updated_at, trigger_type, recall_topic;

-- name: DeletePromptTemplateSection :execrows
DELETE FROM prompt_template_sections
WHERE template_id = $1 AND section_key = $2;
