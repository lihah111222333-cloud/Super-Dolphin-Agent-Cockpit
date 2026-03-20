package sqlc

import "context"

const (
	insertAuditEventSQL = `INSERT INTO audit_events (ts, event_type, action, result, actor, target, detail, level, extra) VALUES (NOW(), $1, $2, $3, $4, $5, $6, $7, $8::jsonb);`
	listAuditEventsSQL  = `SELECT ts, event_type, action, result, actor, target, detail, level, extra FROM audit_events WHERE ($1::text = '' OR event_type = $1) AND ($2::text = '' OR action = $2) AND ($3::text = '' OR actor = $3) AND ($4::text = '' OR event_type ILIKE '%' || $4 || '%' OR action ILIKE '%' || $4 || '%' OR result ILIKE '%' || $4 || '%' OR actor ILIKE '%' || $4 || '%' OR target ILIKE '%' || $4 || '%' OR detail ILIKE '%' || $4 || '%') ORDER BY ts DESC, id DESC LIMIT $5;`
)

func scanAuditEvent(row rowScanner) (AuditEvent, error) {
	var item AuditEvent
	err := row.Scan(&item.Ts, &item.EventType, &item.Action, &item.Result, &item.Actor, &item.Target, &item.Detail, &item.Level, &item.Extra)
	return item, err
}

func (q *Queries) InsertAuditEvent(ctx context.Context, arg InsertAuditEventParams) error {
	return q.exec(ctx, insertAuditEventSQL, arg.EventType, arg.Action, arg.Result, arg.Actor, arg.Target, arg.Detail, arg.Level, arg.Extra)
}

func (q *Queries) ListAuditEvents(ctx context.Context, arg ListAuditEventsParams) ([]AuditEvent, error) {
	return queryMany(ctx, q, listAuditEventsSQL, scanAuditEvent, arg.EventType, arg.Action, arg.Actor, arg.Keyword, arg.Limit)
}
