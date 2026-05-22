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
