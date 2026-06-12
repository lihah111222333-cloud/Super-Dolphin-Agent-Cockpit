-- name: InsertAuditEvent :exec
INSERT INTO audit_events (ts, event_type, action, result, actor, target, detail, level, extra)
VALUES ((CAST(strftime('%s','now') AS INTEGER) * 1000), ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAuditEvents :many
SELECT ts, event_type, action, result, actor, target, detail, level, extra
FROM audit_events
WHERE (? = '' OR event_type = ?)
  AND (? = '' OR action = ?)
  AND (? = '' OR actor = ?)
  AND (? = ''
    OR event_type LIKE '%' || ? || '%'
    OR action LIKE '%' || ? || '%'
    OR result LIKE '%' || ? || '%'
    OR actor LIKE '%' || ? || '%'
    OR target LIKE '%' || ? || '%'
    OR detail LIKE '%' || ? || '%')
ORDER BY ts DESC, id DESC
LIMIT ?;
