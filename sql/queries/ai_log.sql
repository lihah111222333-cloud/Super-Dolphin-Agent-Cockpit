-- name: ListAILogSystemLogs :many
SELECT id, ts, level, logger, message, raw, source, component, agent_id, thread_id, trace_id, event_type, tool_name, duration_ms, extra
FROM system_logs
WHERE ($1::text = '' OR message ILIKE '%' || $1 || '%')
ORDER BY ts DESC, id DESC
LIMIT $2;
