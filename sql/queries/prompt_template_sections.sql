-- name: ListPromptTemplateSectionsByTemplate :many
SELECT id, template_id, section_key, region, ordinal, body, enable_when, enabled,
       created_at, updated_at
FROM prompt_template_sections
WHERE template_id = $1 AND enabled = TRUE
ORDER BY region, ordinal, id;

-- name: UpsertPromptTemplateSection :one
-- Upsert by (template_id, section_key). Touches updated_at on conflict so
-- operators see when they last edited a row. Empty enable_when stays as-is
-- (NULL or '{}' both mean "always inject" per EvaluateEnableWhen).
INSERT INTO prompt_template_sections
    (template_id, section_key, region, ordinal, body, enable_when, enabled)
VALUES
    ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (template_id, section_key) DO UPDATE SET
    region      = EXCLUDED.region,
    ordinal     = EXCLUDED.ordinal,
    body        = EXCLUDED.body,
    enable_when = EXCLUDED.enable_when,
    enabled     = EXCLUDED.enabled,
    updated_at  = NOW()
RETURNING id, template_id, section_key, region, ordinal, body, enable_when, enabled,
          created_at, updated_at;

-- name: DeletePromptTemplateSection :execrows
DELETE FROM prompt_template_sections
WHERE template_id = $1 AND section_key = $2;
