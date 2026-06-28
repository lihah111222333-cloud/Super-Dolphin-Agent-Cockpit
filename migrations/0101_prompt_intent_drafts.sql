BEGIN;

CREATE TABLE IF NOT EXISTS prompt_intent_drafts (
    id BIGSERIAL PRIMARY KEY,
    draft_key TEXT NOT NULL UNIQUE,
    cwd TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL CHECK (kind IN ('expert', 'recall', 'default_rule')),
    raw_input TEXT NOT NULL,
    source_type TEXT NOT NULL DEFAULT 'user_input',
    source_url TEXT NOT NULL DEFAULT '',
    origin_hash TEXT NOT NULL DEFAULT '',
    license_hint TEXT NOT NULL DEFAULT '',
    generated_card JSONB NOT NULL DEFAULT '{}'::jsonb,
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'ready_to_save', 'enabled', 'rejected')),
    scope TEXT NOT NULL DEFAULT 'project' CHECK (scope IN ('project', 'global')),
    issues JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_prompt_intent_drafts_cwd_status_updated
    ON prompt_intent_drafts (cwd, status, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_prompt_intent_drafts_kind_cwd
    ON prompt_intent_drafts (kind, cwd);

-- 0096 created a global unique recall_topic index. Project-scoped recall needs
-- different projects to be able to use the same human-readable topic. Same-CWD
-- duplicates must be rejected by prompt-module write paths because CWD scope
-- lives on prompt_templates.tags rather than a section column.
DROP INDEX IF EXISTS idx_prompt_sections_recall_topic;

CREATE INDEX IF NOT EXISTS idx_prompt_sections_recall_topic_lookup
    ON prompt_template_sections (recall_topic)
    WHERE trigger_type = 'recall' AND recall_topic <> '';

-- Preserve existing platform-seeded recall packs as explicitly global instead
-- of relying on "missing scope means global". Unknown unscoped recall rows are
-- quarantined by disabling their parent template; this avoids a startup-blocking
-- migration while still preventing accidental cross-project injection.
WITH seed_recall_whitelist(prompt_key, section_key, recall_topic) AS (
    VALUES
        ('main/general-zh', 'recall_lsp_basics', 'lsp-basics'),
        ('main/general-zh', 'recall_lsp_advanced', 'lsp-advanced'),
        ('main/general-zh', 'recall_sqlc_workflow', 'sqlc-workflow'),
        ('main/general-zh', 'recall_prompt_template_editing', 'prompt-template-editing'),
        ('main/general-zh', 'recall_frontend_vue3', 'frontend-vue3'),
        ('main/general-zh', 'recall_migration_rules', 'migration-rules'),
        ('main/general-zh', 'recall_guard_rules', 'guard-rules')
),
unscoped_unknown_recall_templates AS (
    SELECT DISTINCT t.id
      FROM prompt_templates t
      JOIN prompt_template_sections s ON s.template_id = t.id
      LEFT JOIN seed_recall_whitelist w
        ON w.prompt_key = t.prompt_key
       AND w.section_key = s.section_key
       AND w.recall_topic = s.recall_topic
     WHERE s.trigger_type = 'recall'
       AND s.recall_topic <> ''
       AND NOT EXISTS (
           SELECT 1
             FROM jsonb_array_elements_text(COALESCE(t.tags, '[]'::jsonb)) tag(value)
            WHERE tag.value LIKE 'scope.cwd:%' OR tag.value = 'scope.global'
       )
       AND w.prompt_key IS NULL
)
UPDATE prompt_templates t
SET enabled = FALSE,
    tags = (
        SELECT jsonb_agg(DISTINCT value)
        FROM (
            SELECT jsonb_array_elements_text(COALESCE(t.tags, '[]'::jsonb)) AS value
            UNION ALL
            SELECT 'quarantine:unscoped-recall'
        ) merged
    ),
    updated_at = NOW()
FROM unscoped_unknown_recall_templates q
WHERE q.id = t.id;

-- Existing seed templates were authored before explicit project/global scope
-- tags. After quarantining unsafe recall rows above, keep every remaining
-- enabled seed-owned template visible to runtime prompt discovery.
UPDATE prompt_templates t
SET tags = (
    SELECT jsonb_agg(DISTINCT value)
    FROM (
        SELECT jsonb_array_elements_text(COALESCE(t.tags, '[]'::jsonb)) AS value
        UNION ALL
        SELECT 'scope.global'
    ) merged
)
WHERE t.created_by IN ('system.seed', 'seed')
  AND t.enabled = TRUE
  AND NOT EXISTS (
    SELECT 1
    FROM jsonb_array_elements_text(COALESCE(t.tags, '[]'::jsonb)) tag(value)
    WHERE tag.value LIKE 'scope.cwd:%' OR tag.value = 'scope.global'
);

COMMIT;
