-- Bus exception logs: structured exception tracking for ACP Bus
-- Categories: tool_timeout, tool_error, client_disconnect, session_stale, crash_restart, unknown

CREATE TABLE IF NOT EXISTS bus_exception_logs (
    id BIGSERIAL PRIMARY KEY,
    ts TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    category TEXT NOT NULL DEFAULT 'unknown',
    severity TEXT NOT NULL DEFAULT 'error',
    source TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    traceback TEXT NOT NULL DEFAULT '',
    extra JSONB
);

CREATE INDEX IF NOT EXISTS idx_bus_exception_logs_ts ON bus_exception_logs (ts DESC);
CREATE INDEX IF NOT EXISTS idx_bus_exception_logs_category ON bus_exception_logs (category);
CREATE INDEX IF NOT EXISTS idx_bus_exception_logs_severity ON bus_exception_logs (severity);
