-- 0005_command_card_versions.sql
-- 命令卡版本归档表：用于页面版本历史与一键回滚

CREATE TABLE IF NOT EXISTS command_card_versions (
    id BIGSERIAL PRIMARY KEY,
    card_key TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    command_template TEXT NOT NULL,
    args_schema JSONB,
    risk_level TEXT NOT NULL DEFAULT 'normal',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    source_updated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_command_card_versions_key_id ON command_card_versions (card_key, id DESC);
