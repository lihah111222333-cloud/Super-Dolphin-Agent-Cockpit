-- name: InsertSystemLog :exec
INSERT INTO system_logs (
    ts, level, logger, message, raw,
    source, component, agent_id, thread_id, trace_id, span_id, parent_span_id,
    event_type, tool_name, duration_ms, extra
)
VALUES (
    sqlc.arg(ts), sqlc.arg(level), sqlc.arg(logger), sqlc.arg(message), sqlc.arg(raw),
    sqlc.arg(source), sqlc.arg(component), sqlc.arg(agent_id), sqlc.arg(thread_id), sqlc.arg(trace_id), sqlc.arg(span_id), sqlc.arg(parent_span_id),
    sqlc.arg(event_type), sqlc.arg(tool_name), sqlc.narg(duration_ms), sqlc.arg(extra)
);

-- name: ListSystemLogs :many
SELECT id, ts, level, logger, message, raw, source, component, agent_id, thread_id, trace_id, span_id, parent_span_id, event_type, tool_name, duration_ms, CAST(extra AS BLOB) AS extra
FROM system_logs
WHERE (sqlc.arg(level_filter) = '' OR level = sqlc.arg(level_filter))
  AND (sqlc.arg(logger_filter) = '' OR logger = sqlc.arg(logger_filter))
  AND (sqlc.arg(source_filter) = '' OR source = sqlc.arg(source_filter))
  AND (sqlc.arg(component_filter) = '' OR component = sqlc.arg(component_filter))
  AND (sqlc.arg(agent_id_filter) = '' OR agent_id = sqlc.arg(agent_id_filter))
  AND (sqlc.arg(thread_id_filter) = '' OR thread_id = sqlc.arg(thread_id_filter))
  AND (sqlc.arg(trace_id_filter) = '' OR trace_id = sqlc.arg(trace_id_filter))
  AND (sqlc.arg(span_id_filter) = '' OR span_id = sqlc.arg(span_id_filter))
  AND (sqlc.arg(parent_span_id_filter) = '' OR parent_span_id = sqlc.arg(parent_span_id_filter))
  AND (sqlc.arg(event_type_filter) = '' OR event_type = sqlc.arg(event_type_filter))
  AND (sqlc.arg(tool_name_filter) = '' OR tool_name = sqlc.arg(tool_name_filter))
  AND (sqlc.arg(keyword) = ''
    OR lower(level) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(logger) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(message) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(raw) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(source) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(component) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(trace_id) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(span_id) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(parent_span_id) LIKE lower(sqlc.arg(keyword_pattern)))
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(limit_count);
