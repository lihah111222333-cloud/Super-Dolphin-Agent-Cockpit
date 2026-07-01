-- name: ListAILogSystemLogs :many
SELECT id, ts, level, logger, message, '' AS raw, source, component, agent_id, thread_id, trace_id, span_id, parent_span_id, event_type, tool_name, duration_ms, '{}' AS extra
FROM system_logs
WHERE (sqlc.arg(keyword) = '' OR lower(message) LIKE lower(sqlc.arg(keyword_pattern)))
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(limit_count);

-- name: ListAILogsByCategory :many
SELECT
    id,
    ts,
    level,
    logger,
    message,
    '' AS raw,
    source,
    component,
    agent_id,
    thread_id,
    trace_id,
    span_id,
    parent_span_id,
    event_type,
    tool_name,
    duration_ms,
    '{}' AS extra
FROM system_logs
WHERE (? = '' OR lower(message) LIKE lower(?))
  AND (
    ? = ''
    OR (CASE
        WHEN lower(message) LIKE '%api request%'
            OR lower(message) LIKE '%request to%'
            OR lower(message) LIKE '%http request%' THEN 'api_request'
        WHEN lower(message) LIKE '%api error%'
            OR lower(message) LIKE '%api_error%' THEN 'api_error'
        WHEN lower(message) LIKE '%compat%'
            OR lower(message) LIKE '%fallback%'
            OR instr(message, char(20860) || char(23481)) > 0 THEN 'compat_fallback'
        WHEN lower(message) LIKE '%runtime%'
            AND lower(message) LIKE '%config%' THEN 'runtime_config'
        WHEN lower(message) LIKE '%error%'
            OR lower(message) LIKE '%exception%' THEN 'error'
        ELSE 'ai_event'
    END) = ?
  )
ORDER BY ts DESC, id DESC
LIMIT ?;

-- name: CountAILogsByStatus :many
SELECT message
FROM system_logs
WHERE lower(message) LIKE '%http/%'
ORDER BY ts DESC, id DESC;

-- name: ListRecentAILogs :many
SELECT
    id,
    ts,
    level,
    logger,
    message,
    '' AS raw,
    source,
    component,
    agent_id,
    thread_id,
    trace_id,
    span_id,
    parent_span_id,
    event_type,
    tool_name,
    duration_ms,
    '{}' AS extra
FROM system_logs
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(limit_count);
