-- hook_pending_review.sql - sqlc queries for hook_pending_reviews table.
-- Migrated from internal/store/hookstore/hookstore.go raw SQL.

-- name: SaveHookPendingReview :execrows
INSERT INTO hook_pending_reviews (
    hook_call_id, topic, agent_id, thread_id, turn_id, subscriber_lease, payload, default_action,
    status, created_at, deadline_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)
ON CONFLICT (hook_call_id) DO NOTHING;

-- name: GetHookPendingReviewForSave :one
SELECT hook_call_id, topic, agent_id, thread_id, turn_id, subscriber_lease, payload, default_action,
       status, created_at, deadline_at
FROM hook_pending_reviews
WHERE hook_call_id = ?;

-- name: GetHookPendingReview :one
SELECT hook_call_id, topic, agent_id, thread_id, turn_id, subscriber_lease, payload, default_action,
       status, created_at, deadline_at
FROM hook_pending_reviews
WHERE hook_call_id = ? AND status = 'pending';

-- name: ListHookPendingReviewsByAgent :many
SELECT hook_call_id, topic, agent_id, thread_id, turn_id, subscriber_lease, payload, default_action,
       status, created_at, deadline_at
FROM hook_pending_reviews
WHERE agent_id = ? AND status = 'pending'
ORDER BY created_at ASC;

-- name: CheckHookReviewIdempotency :one
-- Returns 1 if a review is already resolved with the given idempotency key.
SELECT 1 AS already_resolved
FROM hook_pending_reviews
WHERE hook_call_id = ? AND status = 'resolved' AND idempotency_key = ?;

-- name: ResolveHookPendingReview :execrows
UPDATE hook_pending_reviews
SET status = 'resolved', decision = ?, reason = ?, idempotency_key = ?, resolved_by = ?, resolved_at = ?
WHERE hook_call_id = ? AND status = 'pending';

-- name: GetHookResolvedReview :one
SELECT decision, resolved_at, subscriber_lease
FROM hook_pending_reviews
WHERE hook_call_id = ? AND status = 'resolved';

-- name: CancelHookPendingReviewsByLease :execrows
UPDATE hook_pending_reviews
SET status = 'cancelled', resolved_at = ?
WHERE subscriber_lease = ? AND status = 'pending';

-- name: CancelHookPendingReviewsByAgent :execrows
UPDATE hook_pending_reviews
SET status = 'cancelled', resolved_at = ?
WHERE agent_id = ? AND status = 'pending';

-- name: CancelExpiredHookReviews :execrows
UPDATE hook_pending_reviews
SET status = 'expired', decision = default_action, resolved_at = ?
WHERE status = 'pending' AND deadline_at <= ?;

-- name: RecoverHookPendingReviews :many
SELECT hook_call_id, topic, agent_id, thread_id, turn_id, subscriber_lease, payload, default_action,
       status, created_at, deadline_at
FROM hook_pending_reviews
WHERE status = 'pending'
ORDER BY deadline_at ASC;
