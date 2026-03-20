-- name: ListBusExceptionLogs :many
SELECT ts, category, severity, source, tool_name, message, traceback, extra
FROM bus_exception_logs
WHERE ($1::text = '' OR category = $1)
  AND ($2::text = '' OR severity = $2)
  AND ($3::text = ''
    OR source ILIKE '%' || $3 || '%'
    OR tool_name ILIKE '%' || $3 || '%'
    OR message ILIKE '%' || $3 || '%'
    OR traceback ILIKE '%' || $3 || '%')
ORDER BY ts DESC, id DESC
LIMIT $4;
