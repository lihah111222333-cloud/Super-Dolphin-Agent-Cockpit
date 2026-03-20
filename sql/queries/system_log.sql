-- name: InsertSystemLog :exec
INSERT INTO system_logs (ts, level, logger, message, raw)
VALUES (NOW(), $1, $2, $3, $4);

-- name: ListSystemLogs :many
SELECT id, ts, level, logger, message, raw, source, component, agent_id, thread_id, trace_id, event_type, tool_name, duration_ms, extra
FROM system_logs
WHERE ($1::text = '' OR level = $1)
  AND ($2::text = '' OR logger = $2)
  AND ($3::text = '' OR source = $3)
  AND ($4::text = '' OR component = $4)
  AND ($5::text = '' OR agent_id = $5)
  AND ($6::text = '' OR thread_id = $6)
  AND ($7::text = '' OR event_type = $7)
  AND ($8::text = '' OR tool_name = $8)
  AND ($9::text = ''
    OR level ILIKE '%' || $9 || '%'
    OR logger ILIKE '%' || $9 || '%'
    OR message ILIKE '%' || $9 || '%'
    OR raw ILIKE '%' || $9 || '%'
    OR source ILIKE '%' || $9 || '%'
    OR component ILIKE '%' || $9 || '%')
ORDER BY ts DESC, id DESC
LIMIT $10;
