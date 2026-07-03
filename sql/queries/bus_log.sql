-- name: ListBusExceptionLogs :many
SELECT id, ts, category, severity, source, tool_name, message,
       '' AS traceback,
       '{}' AS extra,
       has_traceback,
       has_extra
FROM bus_exception_logs
WHERE (sqlc.arg(category_filter) = '' OR category = sqlc.arg(category_filter))
  AND (sqlc.arg(severity_filter) = '' OR severity = sqlc.arg(severity_filter))
  AND (sqlc.arg(keyword) = ''
    OR lower(source) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(tool_name) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(message) LIKE lower(sqlc.arg(keyword_pattern)))
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(limit_count);

-- name: GetBusExceptionLog :one
SELECT id, ts, category, severity, source, tool_name, message, traceback, extra,
       has_traceback,
       has_extra
FROM bus_exception_logs
WHERE id = ?;
