-- name: CreateInteraction :one
INSERT INTO agent_interactions (thread_id, parent_id, sender, receiver, msg_type, status, requires_review, payload, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, NOW())
RETURNING id, thread_id, parent_id, sender, receiver, msg_type, status, requires_review, reviewed_by, review_note, reviewed_at, payload, created_at, updated_at;

-- name: GetInteraction :one
SELECT id, thread_id, parent_id, sender, receiver, msg_type, status, requires_review, reviewed_by, review_note, reviewed_at, payload, created_at, updated_at
FROM agent_interactions
WHERE id = $1;

-- name: ListInteractions :many
SELECT id, thread_id, parent_id, sender, receiver, msg_type, status, requires_review, reviewed_by, review_note, reviewed_at, payload, created_at, updated_at
FROM agent_interactions
WHERE ($1::text = '' OR thread_id = $1)
  AND ($2::text = ''
    OR sender ILIKE '%' || $2 || '%'
    OR receiver ILIKE '%' || $2 || '%'
    OR msg_type ILIKE '%' || $2 || '%')
ORDER BY created_at DESC, id DESC
LIMIT $3;

-- name: ReviewInteraction :one
UPDATE agent_interactions
SET status = $1,
    reviewed_by = $2,
    review_note = $3,
    reviewed_at = NOW(),
    updated_at = NOW()
WHERE id = $4
RETURNING id, thread_id, parent_id, sender, receiver, msg_type, status, requires_review, reviewed_by, review_note, reviewed_at, payload, created_at, updated_at;
