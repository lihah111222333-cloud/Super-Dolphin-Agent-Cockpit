-- 0009_system_logs_v2.sql — 统一日志接入: 扩展 system_logs 表支持多源日志。
--
-- 对应 Python 归档 0010_system_logs_v2.sql。
-- 新增 9 列 + 5 个部分索引。
-- 旧数据不受影响 (新列均有默认空值)。

ALTER TABLE system_logs ADD COLUMN IF NOT EXISTS source     TEXT NOT NULL DEFAULT '';
ALTER TABLE system_logs ADD COLUMN IF NOT EXISTS component  TEXT NOT NULL DEFAULT '';
ALTER TABLE system_logs ADD COLUMN IF NOT EXISTS agent_id   TEXT NOT NULL DEFAULT '';
ALTER TABLE system_logs ADD COLUMN IF NOT EXISTS thread_id  TEXT NOT NULL DEFAULT '';
ALTER TABLE system_logs ADD COLUMN IF NOT EXISTS trace_id   TEXT NOT NULL DEFAULT '';
ALTER TABLE system_logs ADD COLUMN IF NOT EXISTS event_type TEXT NOT NULL DEFAULT '';
ALTER TABLE system_logs ADD COLUMN IF NOT EXISTS tool_name  TEXT NOT NULL DEFAULT '';
ALTER TABLE system_logs ADD COLUMN IF NOT EXISTS duration_ms INT;
ALTER TABLE system_logs ADD COLUMN IF NOT EXISTS extra      JSONB;

-- 部分索引: 只索引非空值, 节省空间
CREATE INDEX IF NOT EXISTS idx_system_logs_source   ON system_logs (source)     WHERE source != '';
CREATE INDEX IF NOT EXISTS idx_system_logs_agent    ON system_logs (agent_id)   WHERE agent_id != '';
CREATE INDEX IF NOT EXISTS idx_system_logs_thread   ON system_logs (thread_id)  WHERE thread_id != '';
CREATE INDEX IF NOT EXISTS idx_system_logs_event    ON system_logs (event_type) WHERE event_type != '';
CREATE INDEX IF NOT EXISTS idx_system_logs_tool     ON system_logs (tool_name)  WHERE tool_name != '';
