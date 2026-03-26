-- name: ListAILogSystemLogs :many
SELECT id, ts, level, logger, message, raw, source, component, agent_id, thread_id, trace_id, event_type, tool_name, duration_ms, extra
FROM system_logs
WHERE (sqlc.arg(keyword)::text = '' OR message ILIKE '%' || sqlc.arg(keyword)::text || '%')
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(limit_count);

-- name: ListAILogsByCategory :many
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
            WHEN message ILIKE '%api request%'
                OR message ILIKE '%request to%'
                OR message ILIKE '%http request%' THEN 'api_request'
            WHEN message ILIKE '%api error%'
                OR message ILIKE '%api_error%' THEN 'api_error'
            WHEN message ILIKE '%compat%'
                OR message ILIKE '%fallback%'
                OR message ILIKE '%兼容%' THEN 'compat_fallback'
            WHEN message ILIKE '%runtime%'
                AND message ILIKE '%config%' THEN 'runtime_config'
            WHEN message ILIKE '%error%'
                OR message ILIKE '%exception%' THEN 'error'
            ELSE 'ai_event'
        END AS category,
        COALESCE((regexp_match(message, '(?i)(GET|POST|PUT|DELETE|PATCH|HEAD)[[:space:]]+(https?://[^[:space:]]+)'))[1], '')::text AS method,
        COALESCE((regexp_match(message, '(?i)(GET|POST|PUT|DELETE|PATCH|HEAD)[[:space:]]+(https?://[^[:space:]]+)'))[2], '')::text AS url,
        CASE
            WHEN COALESCE((regexp_match(message, '(?i)(GET|POST|PUT|DELETE|PATCH|HEAD)[[:space:]]+(https?://[^[:space:]]+)'))[2], '') = '' THEN ''
            ELSE regexp_replace(
                COALESCE((regexp_match(message, '(?i)(GET|POST|PUT|DELETE|PATCH|HEAD)[[:space:]]+(https?://[^[:space:]]+)'))[2], ''),
                '^https?://[^/]+',
                ''
            )
        END::text AS endpoint,
        COALESCE((regexp_match(message, '(?i)HTTP/[0-9]\.[0-9][[:space:]]+([0-9]{3})[[:space:]]*([^[:space:]]*)'))[1], '')::text AS status,
        COALESCE((regexp_match(message, '(?i)HTTP/[0-9]\.[0-9][[:space:]]+([0-9]{3})[[:space:]]*([^[:space:]]*)'))[2], '')::text AS status_text,
        COALESCE((regexp_match(message, '(?i)model[=:][[:space:]]*([^[:space:],;"\]]+)'))[1], '')::text AS model
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
    status,
    status_text,
    model
FROM derived_logs
WHERE (sqlc.arg(category)::text = '' OR category = sqlc.arg(category)::text)
  AND (sqlc.arg(keyword)::text = '' OR message ILIKE '%' || sqlc.arg(keyword)::text || '%')
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(limit_count);

-- name: CountAILogsByStatus :many
WITH derived_statuses AS (
    SELECT COALESCE(
        NULLIF(
            (regexp_match(message, '(?i)HTTP/[0-9]\.[0-9][[:space:]]+([0-9]{3})[[:space:]]*([^[:space:]]*)'))[1],
            ''
        ),
        'unknown'
    )::text AS status
    FROM system_logs
)
SELECT status, COUNT(*)::bigint AS count
FROM derived_statuses
GROUP BY status
ORDER BY status ASC;

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
            WHEN message ILIKE '%api request%'
                OR message ILIKE '%request to%'
                OR message ILIKE '%http request%' THEN 'api_request'
            WHEN message ILIKE '%api error%'
                OR message ILIKE '%api_error%' THEN 'api_error'
            WHEN message ILIKE '%compat%'
                OR message ILIKE '%fallback%'
                OR message ILIKE '%兼容%' THEN 'compat_fallback'
            WHEN message ILIKE '%runtime%'
                AND message ILIKE '%config%' THEN 'runtime_config'
            WHEN message ILIKE '%error%'
                OR message ILIKE '%exception%' THEN 'error'
            ELSE 'ai_event'
        END AS category,
        COALESCE((regexp_match(message, '(?i)(GET|POST|PUT|DELETE|PATCH|HEAD)[[:space:]]+(https?://[^[:space:]]+)'))[1], '')::text AS method,
        COALESCE((regexp_match(message, '(?i)(GET|POST|PUT|DELETE|PATCH|HEAD)[[:space:]]+(https?://[^[:space:]]+)'))[2], '')::text AS url,
        CASE
            WHEN COALESCE((regexp_match(message, '(?i)(GET|POST|PUT|DELETE|PATCH|HEAD)[[:space:]]+(https?://[^[:space:]]+)'))[2], '') = '' THEN ''
            ELSE regexp_replace(
                COALESCE((regexp_match(message, '(?i)(GET|POST|PUT|DELETE|PATCH|HEAD)[[:space:]]+(https?://[^[:space:]]+)'))[2], ''),
                '^https?://[^/]+',
                ''
            )
        END::text AS endpoint,
        COALESCE((regexp_match(message, '(?i)HTTP/[0-9]\.[0-9][[:space:]]+([0-9]{3})[[:space:]]*([^[:space:]]*)'))[1], '')::text AS status,
        COALESCE((regexp_match(message, '(?i)HTTP/[0-9]\.[0-9][[:space:]]+([0-9]{3})[[:space:]]*([^[:space:]]*)'))[2], '')::text AS status_text,
        COALESCE((regexp_match(message, '(?i)model[=:][[:space:]]*([^[:space:],;"\]]+)'))[1], '')::text AS model
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
    status,
    status_text,
    model
FROM derived_logs
ORDER BY ts DESC, id DESC
LIMIT $1;
