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
ORDER BY created_at ASC, hook_call_id ASC
LIMIT 500;

-- name: ListHookPendingReviewsByAgentPage :many
SELECT hook_call_id, topic, agent_id, thread_id, turn_id, subscriber_lease, payload, default_action,
       status, created_at, deadline_at
FROM hook_pending_reviews
WHERE agent_id = sqlc.arg(agent_id)
  AND status = 'pending'
  AND (
      sqlc.arg(cursor_created_at) = 0
      OR created_at > sqlc.arg(cursor_created_at)
      OR (created_at = sqlc.arg(cursor_created_at) AND hook_call_id > sqlc.arg(cursor_hook_call_id))
  )
ORDER BY created_at ASC, hook_call_id ASC
LIMIT sqlc.arg(limit) + 1;

-- name: CountHookPendingReviews :one
SELECT COUNT(*)
FROM hook_pending_reviews
WHERE status = 'pending';

-- name: ResolveHookPendingReview :execrows
UPDATE hook_pending_reviews
SET status = CASE WHEN status = 'pending' THEN 'resolved' ELSE status END,
    decision = CASE WHEN status = 'pending' THEN sqlc.arg(decision) ELSE decision END,
    reason = CASE WHEN status = 'pending' THEN sqlc.arg(reason) ELSE reason END,
    idempotency_key = CASE WHEN status = 'pending' THEN sqlc.arg(idempotency_key) ELSE idempotency_key END,
    resolved_by = CASE WHEN status = 'pending' THEN sqlc.arg(resolved_by) ELSE resolved_by END,
    resolved_at = CASE WHEN status = 'pending' THEN sqlc.arg(resolved_at) ELSE resolved_at END
WHERE hook_call_id = sqlc.arg(hook_call_id)
  AND (
      status = 'pending'
      OR (status = 'resolved' AND idempotency_key = sqlc.arg(idempotency_key))
  );

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
