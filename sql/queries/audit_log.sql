-- name: InsertAuditEvent :exec
INSERT INTO audit_events (ts, event_type, action, result, actor, target, detail, level, extra)
VALUES (
    sqlc.arg(ts),
    sqlc.arg(event_type),
    sqlc.arg(action),
    sqlc.arg(result),
    sqlc.arg(actor),
    sqlc.arg(target),
    sqlc.arg(detail),
    sqlc.arg(level),
    sqlc.arg(extra)
);

-- name: ListAuditEvents :many
SELECT id, ts, event_type, action, result, actor, target, detail, level, '{}' AS extra
FROM audit_events
WHERE (sqlc.arg(event_type_filter) = '' OR event_type = sqlc.arg(event_type_filter))
  AND (sqlc.arg(action_filter) = '' OR action = sqlc.arg(action_filter))
  AND (sqlc.arg(actor_filter) = '' OR actor = sqlc.arg(actor_filter))
  AND (sqlc.arg(keyword) = ''
    OR lower(event_type) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(action) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(result) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(actor) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(target) LIKE lower(sqlc.arg(keyword_pattern))
    OR lower(detail) LIKE lower(sqlc.arg(keyword_pattern)))
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(limit_count);
