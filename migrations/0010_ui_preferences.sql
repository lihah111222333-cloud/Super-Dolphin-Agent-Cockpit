-- 0010_ui_preferences.sql — UI 偏好持久化。
-- 取代前端 localStorage, 支持多实例共享。

CREATE TABLE IF NOT EXISTS ui_preferences (
    key         TEXT        PRIMARY KEY,
    value       JSONB       NOT NULL DEFAULT '{}',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 预置默认偏好
INSERT INTO ui_preferences (key, value) VALUES
    ('activeThreadId', '""'::jsonb),
    ('activeCmdThreadId', '""'::jsonb),
    ('mainAgentId', '""'::jsonb),
    ('agentMeta', '{}'::jsonb),
    ('viewPrefs.chat', '{"layout":"focus","splitRatio":64}'::jsonb),
    ('viewPrefs.cmd', '{"layout":"focus","splitRatio":56,"cardCols":3}'::jsonb)
ON CONFLICT (key) DO NOTHING;
