-- name: InsertSystemLog :exec
INSERT INTO system_logs (ts, level, logger, message, raw)
VALUES (sqlc.arg(ts), sqlc.arg(level), sqlc.arg(logger), sqlc.arg(message), sqlc.arg(raw));

-- name: ListSystemLogs :many
SELECT id, ts, level, logger, message, '' AS raw, source, component, agent_id, thread_id, trace_id, event_type, tool_name, duration_ms, '{}' AS extra
FROM system_logs
WHERE (sqlc.arg(level_filter) = '' OR level = sqlc.arg(level_filter))
  AND (sqlc.arg(logger_filter) = '' OR logger = sqlc.arg(logger_filter))
  AND (sqlc.arg(source_filter) = '' OR source = sqlc.arg(source_filter))
  AND (sqlc.arg(component_filter) = '' OR component = sqlc.arg(component_filter))
  AND (sqlc.arg(agent_id_filter) = '' OR agent_id = sqlc.arg(agent_id_filter))
  AND (sqlc.arg(thread_id_filter) = '' OR thread_id = sqlc.arg(thread_id_filter))
  AND (sqlc.arg(event_type_filter) = '' OR event_type = sqlc.arg(event_type_filter))
  AND (sqlc.arg(tool_name_filter) = '' OR tool_name = sqlc.arg(tool_name_filter))
  AND (sqlc.arg(keyword) = ''
    OR lower(level) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(logger) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(message) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(raw) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(source) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(component) LIKE lower(sqlc.arg(keyword_pattern)))
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(limit_count);
