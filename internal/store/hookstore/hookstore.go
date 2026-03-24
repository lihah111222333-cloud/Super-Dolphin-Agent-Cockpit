package hookstore

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// Compile-time interface check.
var _ contract.HookReviewStore = (*Store)(nil)

// hookstore uses handwritten SQL via sqlc.Queryable instead of generated sqlc
// queries because hook_pending_reviews requires dynamic status transitions and
// idempotency logic that do not map cleanly to sqlc's static query model. This
// is an explicit exception to the sqlc convention documented in
// docs/契约/sqlc-convention.md.
//
// Store implements contract.HookReviewStore for hook_pending_reviews.
type Store struct {
	db sqlc.Queryable
}

// NewStore creates a new HookReviewStore backed by the shared sqlc query handle.
func NewStore(q *sqlc.Queries) contract.HookReviewStore {
	return &Store{db: q.Queryable()}
}

type scanRow interface {
	Scan(dest ...any) error
}

type scanRows interface {
	Close()
	Err() error
	Next() bool
	Scan(dest ...any) error
}

// SavePendingReview inserts a new pending hook review row.
func (s *Store) SavePendingReview(ctx context.Context, review mcp.PendingHookReview) error {
	const q = `
		INSERT INTO hook_pending_reviews (
			hook_call_id, topic, agent_id, subscriber_lease, default_action,
			status, created_at, deadline_at
		) VALUES ($1, $2, $3, $4, $5, 'pending', $6, $7)
		ON CONFLICT (hook_call_id) DO NOTHING
	`
	_, err := s.db.Exec(ctx, q,
		review.HookCallID,
		review.Topic,
		review.AgentID,
		review.SubscriberLease,
		review.DefaultAction,
		review.CreatedAt,
		review.DeadlineAt,
	)
	return wrapErr(err, "save")
}

// GetPendingReview retrieves a single pending hook review by its call ID.
func (s *Store) GetPendingReview(ctx context.Context, hookCallID string) (mcp.PendingHookReview, error) {
	const q = `
		SELECT hook_call_id, topic, agent_id, subscriber_lease, default_action,
		       status, created_at, deadline_at
		FROM hook_pending_reviews
		WHERE hook_call_id = $1 AND status = 'pending'
	`
	row := s.db.QueryRow(ctx, q, hookCallID)
	r, err := scanReview(row)
	if err != nil {
		if platformdb.IsNotFound(err) {
			err = contract.ErrHookReviewNotFound
		}
		return mcp.PendingHookReview{}, wrapErr(err, "get")
	}
	return r, nil
}

// ListPendingReviews returns all pending reviews for a given agent.
func (s *Store) ListPendingReviews(ctx context.Context, agentID string) ([]mcp.PendingHookReview, error) {
	const q = `
		SELECT hook_call_id, topic, agent_id, subscriber_lease, default_action,
		       status, created_at, deadline_at
		FROM hook_pending_reviews
		WHERE agent_id = $1 AND status = 'pending'
		ORDER BY created_at ASC
	`
	rows, err := s.db.Query(ctx, q, agentID)
	if err != nil {
		return nil, wrapErr(err, "list")
	}
	defer rows.Close()
	var result []mcp.PendingHookReview
	for rows.Next() {
		r, err := scanReviewRows(rows)
		if err != nil {
			return nil, wrapErr(err, "list.scan")
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapErr(err, "list.iter")
	}
	return result, nil
}

// ResolvePendingReview marks a pending review as resolved with the given decision.
func (s *Store) ResolvePendingReview(ctx context.Context, hookCallID, decision, reason, idempotencyKey, resolvedBy string) error {
	const qCheck = `
		SELECT 1
		FROM hook_pending_reviews
		WHERE hook_call_id = $1 AND status = 'resolved' AND idempotency_key = $2
	`
	var alreadyResolved int
	if err := s.db.QueryRow(ctx, qCheck, hookCallID, idempotencyKey).Scan(&alreadyResolved); err == nil {
		return nil
	} else if !platformdb.IsNotFound(err) {
		return wrapErr(err, "resolve.idempotency_check")
	}

	const q = `
		UPDATE hook_pending_reviews
		SET status = 'resolved', decision = $2, reason = $3, idempotency_key = $4, resolved_by = $5, resolved_at = $6
		WHERE hook_call_id = $1 AND status = 'pending'
	`
	tag, err := s.db.Exec(ctx, q, hookCallID, decision, reason, idempotencyKey, resolvedBy, time.Now().UTC())
	if err != nil {
		return wrapErr(err, "resolve")
	}
	if tag.RowsAffected() == 0 {
		return wrapErr(contract.ErrHookReviewNotFound, "resolve")
	}
	return nil
}

// GetResolvedReview returns the canonical decision metadata plus subscriber lease for a resolved review.
func (s *Store) GetResolvedReview(ctx context.Context, hookCallID string) (string, time.Time, string, error) {
	const q = `
		SELECT decision, resolved_at, subscriber_lease
		FROM hook_pending_reviews
		WHERE hook_call_id = $1 AND status = 'resolved'
	`
	var decision string
	var resolvedAt time.Time
	var subscriberLease string
	row := s.db.QueryRow(ctx, q, hookCallID)
	if err := row.Scan(&decision, &resolvedAt, &subscriberLease); err != nil {
		if platformdb.IsNotFound(err) {
			err = contract.ErrHookReviewNotFound
		}
		return "", time.Time{}, "", wrapErr(err, "get_resolved")
	}
	return decision, resolvedAt, subscriberLease, nil
}

// CancelPendingReviewsByLease marks all pending reviews for the given subscriber lease as cancelled.
func (s *Store) CancelPendingReviewsByLease(ctx context.Context, subscriberLease string) (int, error) {
	const q = `
		UPDATE hook_pending_reviews
		SET status = 'cancelled', resolved_at = $2
		WHERE subscriber_lease = $1 AND status = 'pending'
	`
	tag, err := s.db.Exec(ctx, q, subscriberLease, time.Now().UTC())
	if err != nil {
		return 0, wrapErr(err, "cancel_by_lease")
	}
	return int(tag.RowsAffected()), nil
}

// CancelPendingReviewsByAgent marks all pending reviews for the given agent as cancelled.
func (s *Store) CancelPendingReviewsByAgent(ctx context.Context, agentID string) (int, error) {
	const q = `
		UPDATE hook_pending_reviews
		SET status = 'cancelled', resolved_at = $2
		WHERE agent_id = $1 AND status = 'pending'
	`
	tag, err := s.db.Exec(ctx, q, agentID, time.Now().UTC())
	if err != nil {
		return 0, wrapErr(err, "cancel_by_agent")
	}
	return int(tag.RowsAffected()), nil
}

// CancelExpiredReviews transitions all expired pending reviews to their default action.
// Returns the number of reviews cancelled.
func (s *Store) CancelExpiredReviews(ctx context.Context) (int, error) {
	const q = `
		UPDATE hook_pending_reviews
		SET status = 'expired', decision = default_action, resolved_at = $1
		WHERE status = 'pending' AND deadline_at <= $1
	`
	tag, err := s.db.Exec(ctx, q, time.Now().UTC())
	if err != nil {
		return 0, wrapErr(err, "cancel_expired")
	}
	return int(tag.RowsAffected()), nil
}

// RecoverOnStartup returns all reviews that are still pending (used at process start).
func (s *Store) RecoverOnStartup(ctx context.Context) ([]mcp.PendingHookReview, error) {
	const q = `
		SELECT hook_call_id, topic, agent_id, subscriber_lease, default_action,
		       status, created_at, deadline_at
		FROM hook_pending_reviews
		WHERE status = 'pending'
		ORDER BY deadline_at ASC
	`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, wrapErr(err, "recover")
	}
	defer rows.Close()
	var result []mcp.PendingHookReview
	for rows.Next() {
		r, err := scanReviewRows(rows)
		if err != nil {
			return nil, wrapErr(err, "recover.scan")
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapErr(err, "recover.iter")
	}
	return result, nil
}

// --- internal helpers ---

func scanReview(row scanRow) (mcp.PendingHookReview, error) {
	var r mcp.PendingHookReview
	var status string
	err := row.Scan(
		&r.HookCallID, &r.Topic, &r.AgentID, &r.SubscriberLease, &r.DefaultAction,
		&status, &r.CreatedAt, &r.DeadlineAt,
	)
	return r, err
}

func scanReviewRows(rows scanRows) (mcp.PendingHookReview, error) {
	var r mcp.PendingHookReview
	var status string
	err := rows.Scan(
		&r.HookCallID, &r.Topic, &r.AgentID, &r.SubscriberLease, &r.DefaultAction,
		&status, &r.CreatedAt, &r.DeadlineAt,
	)
	return r, err
}

func wrapErr(err error, op string) error {
	return platformdb.WrapStoreError(err, op, "hook_pending_review")
}
