-- name: CreateInteraction :one
INSERT INTO agent_interactions (thread_id, parent_id, sender, receiver, msg_type, status, requires_review, payload, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000))
RETURNING id, thread_id, parent_id, sender, receiver, msg_type, status, requires_review, reviewed_by, review_note, reviewed_at, payload, created_at, updated_at;

-- name: GetInteraction :one
SELECT id, thread_id, parent_id, sender, receiver, msg_type, status, requires_review, reviewed_by, review_note, reviewed_at, payload, created_at, updated_at
FROM agent_interactions
WHERE id = ?;

-- name: ListInteractions :many
SELECT id, thread_id, parent_id, sender, receiver, msg_type, status, requires_review, reviewed_by, review_note, reviewed_at, payload, created_at, updated_at
FROM agent_interactions
WHERE (? = '' OR thread_id = ?)
  AND (? = ''
    OR sender LIKE '%' || ? || '%'
    OR receiver LIKE '%' || ? || '%'
    OR msg_type LIKE '%' || ? || '%')
ORDER BY created_at DESC, id DESC
LIMIT ?;

-- name: ReviewInteraction :one
UPDATE agent_interactions
SET status = ?,
    reviewed_by = ?,
    review_note = ?,
    reviewed_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE id = ? AND status = 'pending' AND requires_review = 1
RETURNING id, thread_id, parent_id, sender, receiver, msg_type, status, requires_review, reviewed_by, review_note, reviewed_at, payload, created_at, updated_at;
