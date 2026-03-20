package sqlc

import "context"

const (
	createInteractionSQL = `INSERT INTO agent_interactions (thread_id, parent_id, sender, receiver, msg_type, status, requires_review, payload, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, NOW()) RETURNING id, thread_id, parent_id, sender, receiver, msg_type, status, requires_review, reviewed_by, review_note, reviewed_at, payload, created_at, updated_at;`
	getInteractionSQL    = `SELECT id, thread_id, parent_id, sender, receiver, msg_type, status, requires_review, reviewed_by, review_note, reviewed_at, payload, created_at, updated_at FROM agent_interactions WHERE id = $1;`
	listInteractionsSQL  = `SELECT id, thread_id, parent_id, sender, receiver, msg_type, status, requires_review, reviewed_by, review_note, reviewed_at, payload, created_at, updated_at FROM agent_interactions WHERE ($1::text = '' OR thread_id = $1) AND ($2::text = '' OR sender ILIKE '%' || $2 || '%' OR receiver ILIKE '%' || $2 || '%' OR msg_type ILIKE '%' || $2 || '%') ORDER BY created_at DESC, id DESC LIMIT $3;`
	reviewInteractionSQL = `UPDATE agent_interactions SET status = $1, reviewed_by = $2, review_note = $3, reviewed_at = NOW(), updated_at = NOW() WHERE id = $4 RETURNING id, thread_id, parent_id, sender, receiver, msg_type, status, requires_review, reviewed_by, review_note, reviewed_at, payload, created_at, updated_at;`
)

func scanAgentInteraction(row rowScanner) (AgentInteraction, error) {
	var item AgentInteraction
	err := row.Scan(&item.ID, &item.ThreadID, &item.ParentID, &item.Sender, &item.Receiver, &item.MsgType, &item.Status, &item.RequiresReview, &item.ReviewedBy, &item.ReviewNote, &item.ReviewedAt, &item.Payload, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func (q *Queries) CreateInteraction(ctx context.Context, arg CreateInteractionParams) (AgentInteraction, error) {
	return queryOne(ctx, q, createInteractionSQL, scanAgentInteraction, arg.ThreadID, arg.ParentID, arg.Sender, arg.Receiver, arg.MsgType, arg.Status, arg.RequiresReview, arg.Payload)
}

func (q *Queries) GetInteraction(ctx context.Context, id int64) (AgentInteraction, error) {
	return queryOne(ctx, q, getInteractionSQL, scanAgentInteraction, id)
}

func (q *Queries) ListInteractions(ctx context.Context, arg ListInteractionsParams) ([]AgentInteraction, error) {
	return queryMany(ctx, q, listInteractionsSQL, scanAgentInteraction, arg.ThreadID, arg.Keyword, arg.Limit)
}

func (q *Queries) ReviewInteraction(ctx context.Context, arg ReviewInteractionParams) (AgentInteraction, error) {
	return queryOne(ctx, q, reviewInteractionSQL, scanAgentInteraction, arg.Status, arg.ReviewedBy, arg.ReviewNote, arg.ID)
}
