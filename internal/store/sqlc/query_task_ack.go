package sqlc

import "context"

const (
	upsertTaskAckSQL = `INSERT INTO task_acks ( ack_key, title, description, assigned_to, requested_by, priority, status, progress, ack_message, result_summary, metadata, due_at ) VALUES ($1, $2, $3, $4, $5, $6, $7, GREATEST(0, LEAST($8, 100)), $9, $10, $11::jsonb, $12::timestamptz) ON CONFLICT (ack_key) DO UPDATE SET title = EXCLUDED.title, description = EXCLUDED.description, assigned_to = EXCLUDED.assigned_to, requested_by = EXCLUDED.requested_by, priority = EXCLUDED.priority, status = EXCLUDED.status, progress = EXCLUDED.progress, ack_message = EXCLUDED.ack_message, result_summary = EXCLUDED.result_summary, metadata = EXCLUDED.metadata, due_at = EXCLUDED.due_at, updated_at = NOW() RETURNING id, ack_key, title, description, assigned_to, requested_by, priority, status, progress, ack_message, result_summary, metadata, due_at, acked_at, started_at, finished_at, created_at, updated_at;`
	listTaskAcksSQL  = `SELECT id, ack_key, title, description, assigned_to, requested_by, priority, status, progress, ack_message, result_summary, metadata, due_at, acked_at, started_at, finished_at, created_at, updated_at FROM task_acks WHERE ($1::text = '' OR status = $1) AND ($2::text = '' OR priority = $2) AND ($3::text = '' OR assigned_to = $3) AND ($4::text = '' OR ack_key ILIKE '%' || $4 || '%' OR title ILIKE '%' || $4 || '%' OR description ILIKE '%' || $4 || '%') ORDER BY updated_at DESC, id DESC LIMIT $5;`
)

func scanTaskAck(row rowScanner) (TaskAck, error) {
	var item TaskAck
	err := row.Scan(&item.ID, &item.AckKey, &item.Title, &item.Description, &item.AssignedTo, &item.RequestedBy, &item.Priority, &item.Status, &item.Progress, &item.AckMessage, &item.ResultSummary, &item.Metadata, &item.DueAt, &item.AckedAt, &item.StartedAt, &item.FinishedAt, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func (q *Queries) UpsertTaskAck(ctx context.Context, arg UpsertTaskAckParams) (TaskAck, error) {
	return queryOne(ctx, q, upsertTaskAckSQL, scanTaskAck, arg.AckKey, arg.Title, arg.Description, arg.AssignedTo, arg.RequestedBy, arg.Priority, arg.Status, arg.Progress, arg.AckMessage, arg.ResultSummary, arg.Metadata, arg.DueAt)
}

func (q *Queries) ListTaskAcks(ctx context.Context, arg ListTaskAcksParams) ([]TaskAck, error) {
	return queryMany(ctx, q, listTaskAcksSQL, scanTaskAck, arg.Status, arg.Priority, arg.AssignedTo, arg.Keyword, arg.Limit)
}
