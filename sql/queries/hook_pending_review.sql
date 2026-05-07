-- hook_pending_review.sql — sqlc queries for hook_pending_reviews table.
-- Migrated from internal/store/hookstore/hookstore.go raw SQL.

-- name: SaveHookPendingReview :exec
INSERT INTO hook_pending_reviews (
    hook_call_id, topic, agent_id, subscriber_lease, default_action,
    status, created_at, deadline_at
) VALUES ($1, $2, $3, $4, $5, 'pending', $6, $7)
ON CONFLICT (hook_call_id) DO NOTHING;

-- name: GetHookPendingReview :one
SELECT hook_call_id, topic, agent_id, subscriber_lease, default_action,
       status, created_at, deadline_at
FROM hook_pending_reviews
WHERE hook_call_id = $1 AND status = 'pending';

-- name: ListHookPendingReviewsByAgent :many
SELECT hook_call_id, topic, agent_id, subscriber_lease, default_action,
       status, created_at, deadline_at
FROM hook_pending_reviews
WHERE agent_id = $1 AND status = 'pending'
ORDER BY created_at ASC;

-- name: CheckHookReviewIdempotency :one
-- Returns 1 if a review is already resolved with the given idempotency key.
SELECT 1::int AS already_resolved
FROM hook_pending_reviews
WHERE hook_call_id = $1 AND status = 'resolved' AND idempotency_key = $2;

-- name: ResolveHookPendingReview :execrows
UPDATE hook_pending_reviews
SET status = 'resolved', decision = $2, reason = $3, idempotency_key = $4, resolved_by = $5, resolved_at = $6
WHERE hook_call_id = $1 AND status = 'pending';

-- name: GetHookResolvedReview :one
SELECT decision, resolved_at, subscriber_lease
FROM hook_pending_reviews
WHERE hook_call_id = $1 AND status = 'resolved';

-- name: CancelHookPendingReviewsByLease :execrows
UPDATE hook_pending_reviews
SET status = 'cancelled', resolved_at = $2
WHERE subscriber_lease = $1 AND status = 'pending';

-- name: CancelHookPendingReviewsByAgent :execrows
UPDATE hook_pending_reviews
SET status = 'cancelled', resolved_at = $2
WHERE agent_id = $1 AND status = 'pending';

-- name: CancelExpiredHookReviews :execrows
UPDATE hook_pending_reviews
SET status = 'expired', decision = default_action, resolved_at = $1
WHERE status = 'pending' AND deadline_at <= $1;

-- name: RecoverHookPendingReviews :many
SELECT hook_call_id, topic, agent_id, subscriber_lease, default_action,
       status, created_at, deadline_at
FROM hook_pending_reviews
WHERE status = 'pending'
ORDER BY deadline_at ASC;
