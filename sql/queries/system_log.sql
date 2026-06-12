-- name: InsertSystemLog :exec
INSERT INTO system_logs (ts, level, logger, message, raw)
VALUES ((CAST(strftime('%s','now') AS INTEGER) * 1000), ?, ?, ?, ?);

-- name: ListSystemLogs :many
SELECT id, ts, level, logger, message, raw, source, component, agent_id, thread_id, trace_id, event_type, tool_name, duration_ms, extra
FROM system_logs
WHERE (? = '' OR level = ?)
  AND (? = '' OR logger = ?)
  AND (? = '' OR source = ?)
  AND (? = '' OR component = ?)
  AND (? = '' OR agent_id = ?)
  AND (? = '' OR thread_id = ?)
  AND (? = '' OR event_type = ?)
  AND (? = '' OR tool_name = ?)
  AND (? = ''
    OR level LIKE '%' || ? || '%'
    OR logger LIKE '%' || ? || '%'
    OR message LIKE '%' || ? || '%'
    OR raw LIKE '%' || ? || '%'
    OR source LIKE '%' || ? || '%'
    OR component LIKE '%' || ? || '%')
ORDER BY ts DESC, id DESC
LIMIT ?;
