DROP INDEX IF EXISTS idx_prompt_templates_agent_tool;
-- SPLIT --
DROP INDEX IF EXISTS idx_prompt_templates_enabled;
-- SPLIT --
DROP INDEX IF EXISTS idx_prompt_templates_auto_route;
-- SPLIT --
CREATE TABLE prompt_templates_new (
    id INTEGER PRIMARY KEY,
    prompt_key TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL DEFAULT '',
    agent_key TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT '',
    prompt_text TEXT NOT NULL,
    variables TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(variables)),
    tags TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(tags)),
    description TEXT NOT NULL DEFAULT '',
    when_to_use TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
    manually_edited INTEGER NOT NULL DEFAULT 0 CHECK(manually_edited IN (0, 1)),
    match_when TEXT CHECK(match_when IS NULL OR (json_valid(match_when) AND json_type(match_when) = 'object')),
    priority INTEGER NOT NULL DEFAULT 0,
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
-- SPLIT --
INSERT INTO prompt_templates_new (
    id, prompt_key, title, agent_key, tool_name, prompt_text,
    variables, tags, description, when_to_use, enabled, manually_edited,
    match_when, priority, created_by, updated_by, created_at, updated_at
)
SELECT
    id, prompt_key, title, agent_key, tool_name, prompt_text,
    variables, tags, description, when_to_use, enabled, manually_edited,
    CASE
        WHEN match_when IS NULL OR trim(match_when) = '' OR trim(match_when) = 'null' THEN NULL
        ELSE match_when
    END,
    priority, created_by, updated_by, created_at, updated_at
FROM prompt_templates;
-- SPLIT --
DROP TABLE prompt_templates;
-- SPLIT --
ALTER TABLE prompt_templates_new RENAME TO prompt_templates;
-- SPLIT --
CREATE INDEX IF NOT EXISTS idx_prompt_templates_agent_tool ON prompt_templates(agent_key, tool_name);
-- SPLIT --
CREATE INDEX IF NOT EXISTS idx_prompt_templates_enabled ON prompt_templates(enabled, updated_at DESC);
-- SPLIT --
CREATE INDEX IF NOT EXISTS idx_prompt_templates_auto_route ON prompt_templates(enabled, priority DESC) WHERE match_when IS NOT NULL AND match_when <> '{}';
