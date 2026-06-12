-- name: ListBusExceptionLogs :many
SELECT ts, category, severity, source, tool_name, message, traceback, extra
FROM bus_exception_logs
WHERE (? = '' OR category = ?)
  AND (? = '' OR severity = ?)
  AND (? = ''
    OR source LIKE '%' || ? || '%'
    OR tool_name LIKE '%' || ? || '%'
    OR message LIKE '%' || ? || '%'
    OR traceback LIKE '%' || ? || '%')
ORDER BY ts DESC, id DESC
LIMIT ?;
