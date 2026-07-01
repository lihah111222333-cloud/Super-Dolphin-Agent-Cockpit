CREATE TABLE system_logs_trace_span_migration (
    id INTEGER PRIMARY KEY,
    ts INTEGER NOT NULL,
    level TEXT NOT NULL,
    logger TEXT NOT NULL,
    message TEXT NOT NULL,
    raw TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',
    component TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    thread_id TEXT NOT NULL DEFAULT '',
    trace_id TEXT NOT NULL DEFAULT '',
    span_id TEXT NOT NULL DEFAULT '',
    parent_span_id TEXT NOT NULL DEFAULT '',
    event_type TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER,
    extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra))
);
-- SPLIT --
INSERT INTO system_logs_trace_span_migration (
    id, ts, level, logger, message, raw,
    source, component, agent_id, thread_id, trace_id,
    span_id, parent_span_id,
    event_type, tool_name, duration_ms, extra
)
SELECT
    id, ts, level, logger, message, raw,
    source, component, agent_id, thread_id, trace_id,
    '', '',
    event_type, tool_name, duration_ms, COALESCE(extra, '{}')
FROM system_logs;
-- SPLIT --
DROP TABLE system_logs;
-- SPLIT --
ALTER TABLE system_logs_trace_span_migration RENAME TO system_logs;
-- SPLIT --
CREATE INDEX IF NOT EXISTS idx_system_logs_ts_id ON system_logs(ts DESC, id DESC);
-- SPLIT --
CREATE INDEX IF NOT EXISTS idx_system_logs_level_ts_id ON system_logs(level, ts DESC, id DESC);
-- SPLIT --
CREATE INDEX IF NOT EXISTS idx_system_logs_source_ts_id ON system_logs(source, ts DESC, id DESC) WHERE source <> '';
-- SPLIT --
CREATE INDEX IF NOT EXISTS idx_system_logs_agent_ts_id ON system_logs(agent_id, ts DESC, id DESC) WHERE agent_id <> '';
-- SPLIT --
CREATE INDEX IF NOT EXISTS idx_system_logs_thread_ts_id ON system_logs(thread_id, ts DESC, id DESC) WHERE thread_id <> '';
-- SPLIT --
CREATE INDEX IF NOT EXISTS idx_system_logs_trace_ts_id ON system_logs(trace_id, ts DESC, id DESC) WHERE trace_id <> '';
-- SPLIT --
CREATE INDEX IF NOT EXISTS idx_system_logs_span_ts_id ON system_logs(span_id, ts DESC, id DESC) WHERE span_id <> '';
-- SPLIT --
CREATE INDEX IF NOT EXISTS idx_system_logs_logger ON system_logs(logger);
-- SPLIT --
CREATE INDEX IF NOT EXISTS idx_system_logs_event ON system_logs(event_type) WHERE event_type <> '';
-- SPLIT --
CREATE INDEX IF NOT EXISTS idx_system_logs_tool ON system_logs(tool_name) WHERE tool_name <> '';
