-- name: InsertAuditEvent :exec
INSERT INTO audit_events (ts, event_type, action, result, actor, target, detail, level, extra)
VALUES (NOW(), $1, $2, $3, $4, $5, $6, $7, $8::jsonb);

-- name: ListAuditEvents :many
SELECT ts, event_type, action, result, actor, target, detail, level, extra
FROM audit_events
WHERE ($1::text = '' OR event_type = $1)
  AND ($2::text = '' OR action = $2)
  AND ($3::text = '' OR actor = $3)
  AND ($4::text = ''
    OR event_type ILIKE '%' || $4 || '%'
    OR action ILIKE '%' || $4 || '%'
    OR result ILIKE '%' || $4 || '%'
    OR actor ILIKE '%' || $4 || '%'
    OR target ILIKE '%' || $4 || '%'
    OR detail ILIKE '%' || $4 || '%')
ORDER BY ts DESC, id DESC
LIMIT $5;
