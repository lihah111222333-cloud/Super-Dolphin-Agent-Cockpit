-- name: ListAILogSystemLogs :many
SELECT id, ts, level, logger, message, raw, source, component, agent_id, thread_id, trace_id, event_type, tool_name, duration_ms, extra
FROM system_logs
WHERE (? = '' OR message LIKE '%' || ? || '%')
ORDER BY ts DESC, id DESC
LIMIT ?;

-- name: ListAILogsByCategory :many
SELECT
    id,
    ts,
    level,
    logger,
    message,
    raw,
    source,
    component,
    agent_id,
    thread_id,
    trace_id,
    event_type,
    tool_name,
    duration_ms,
    extra,
    CASE
        WHEN message LIKE '%api request%'
            OR message LIKE '%request to%'
            OR message LIKE '%http request%' THEN 'api_request'
        WHEN message LIKE '%api error%'
            OR message LIKE '%api_error%' THEN 'api_error'
        WHEN message LIKE '%compat%'
            OR message LIKE '%fallback%'
            OR message LIKE '%兼容%' THEN 'compat_fallback'
        WHEN message LIKE '%runtime%'
            AND message LIKE '%config%' THEN 'runtime_config'
        WHEN message LIKE '%error%'
            OR message LIKE '%exception%' THEN 'error'
        ELSE 'ai_event'
    END AS category,
    '' AS method,
    '' AS url,
    '' AS endpoint,
    '' AS http_status,
    '' AS status_text,
    '' AS model
FROM system_logs
WHERE (? = '' OR (
    CASE
        WHEN message LIKE '%api request%' OR message LIKE '%request to%' OR message LIKE '%http request%' THEN 'api_request'
        WHEN message LIKE '%api error%' OR message LIKE '%api_error%' THEN 'api_error'
        WHEN message LIKE '%compat%' OR message LIKE '%fallback%' OR message LIKE '%兼容%' THEN 'compat_fallback'
        WHEN message LIKE '%runtime%' AND message LIKE '%config%' THEN 'runtime_config'
        WHEN message LIKE '%error%' OR message LIKE '%exception%' THEN 'error'
        ELSE 'ai_event'
    END = ?))
  AND (? = '' OR message LIKE '%' || ? || '%')
ORDER BY ts DESC, id DESC
LIMIT ?;

-- name: CountAILogsByStatus :many
SELECT level AS http_status, COUNT(*) AS count
FROM system_logs
GROUP BY level
ORDER BY level ASC;

-- name: ListRecentAILogs :many
WITH derived_logs AS (
    SELECT
        id,
        ts,
        level,
        logger,
        message,
        raw,
        source,
        component,
        agent_id,
        thread_id,
        trace_id,
        event_type,
        tool_name,
        duration_ms,
        extra,
        CASE
            WHEN message LIKE '%api request%'
                OR message LIKE '%request to%'
                OR message LIKE '%http request%' THEN 'api_request'
            WHEN message LIKE '%api error%'
                OR message LIKE '%api_error%' THEN 'api_error'
            WHEN message LIKE '%compat%'
                OR message LIKE '%fallback%'
                OR message LIKE '%兼容%' THEN 'compat_fallback'
            WHEN message LIKE '%runtime%'
                AND message LIKE '%config%' THEN 'runtime_config'
            WHEN message LIKE '%error%'
                OR message LIKE '%exception%' THEN 'error'
            ELSE 'ai_event'
        END AS category,
        '' AS method,
        '' AS url,
        '' AS endpoint,
        '' AS http_status,
        '' AS status_text,
        '' AS model
    FROM system_logs
)
SELECT
    id,
    ts,
    level,
    logger,
    message,
    raw,
    source,
    component,
    agent_id,
    thread_id,
    trace_id,
    event_type,
    tool_name,
    duration_ms,
    extra,
    category,
    method,
    url,
    endpoint,
    http_status,
    status_text,
    model
FROM derived_logs
ORDER BY ts DESC, id DESC
LIMIT ?;
