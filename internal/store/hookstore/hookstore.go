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
var _ contract.HookReviewStore = (*store)(nil)

type querier interface {
	SaveHookPendingReview(ctx context.Context, arg sqlc.SaveHookPendingReviewParams) error
	GetHookPendingReview(ctx context.Context, arg sqlc.GetHookPendingReviewParams) (sqlc.GetHookPendingReviewRow, error)
	ListHookPendingReviewsByAgent(ctx context.Context, arg sqlc.ListHookPendingReviewsByAgentParams) ([]sqlc.ListHookPendingReviewsByAgentRow, error)
	CheckHookReviewIdempotency(ctx context.Context, arg sqlc.CheckHookReviewIdempotencyParams) (int64, error)
	ResolveHookPendingReview(ctx context.Context, arg sqlc.ResolveHookPendingReviewParams) (int64, error)
	GetHookResolvedReview(ctx context.Context, arg sqlc.GetHookResolvedReviewParams) (sqlc.GetHookResolvedReviewRow, error)
	CancelHookPendingReviewsByLease(ctx context.Context, arg sqlc.CancelHookPendingReviewsByLeaseParams) (int64, error)
	CancelHookPendingReviewsByAgent(ctx context.Context, arg sqlc.CancelHookPendingReviewsByAgentParams) (int64, error)
	CancelExpiredHookReviews(ctx context.Context, arg sqlc.CancelExpiredHookReviewsParams) (int64, error)
	RecoverHookPendingReviews(ctx context.Context) ([]sqlc.RecoverHookPendingReviewsRow, error)
}

// store implements contract.HookReviewStore for hook_pending_reviews.
type store struct {
	q querier
}

// NewStore creates a new HookReviewStore backed by generated sqlc queries.
func NewStore(q *sqlc.Queries) contract.HookReviewStore {
	if q == nil {
		return &store{}
	}
	return &store{q: q}
}

func newStoreForTest(q querier) *store {
	return &store{q: q}
}

// SavePendingReview inserts a new pending hook review row.
func (s *store) SavePendingReview(ctx context.Context, review mcp.PendingHookReview) error {
	err := s.q.SaveHookPendingReview(ctx, sqlc.SaveHookPendingReviewParams{
		HookCallID:      review.HookCallID,
		Topic:           review.Topic,
		AgentID:         review.AgentID,
		SubscriberLease: review.SubscriberLease,
		DefaultAction:   review.DefaultAction,
		CreatedAt:       toMS(review.CreatedAt),
		DeadlineAt:      toMS(review.DeadlineAt),
	})
	return wrapErr(err, "save")
}

// GetPendingReview retrieves a single pending hook review by its call ID.
func (s *store) GetPendingReview(ctx context.Context, hookCallID string) (mcp.PendingHookReview, error) {
	row, err := s.q.GetHookPendingReview(ctx, sqlc.GetHookPendingReviewParams{HookCallID: hookCallID})
	if err != nil {
		if platformdb.IsNotFound(err) {
			err = contract.ErrHookReviewNotFound
		}
		return mcp.PendingHookReview{}, wrapErr(err, "get")
	}
	return pendingFromGet(row), nil
}

// ListPendingReviews returns all pending reviews for a given agent.
func (s *store) ListPendingReviews(ctx context.Context, agentID string) ([]mcp.PendingHookReview, error) {
	rows, err := s.q.ListHookPendingReviewsByAgent(ctx, sqlc.ListHookPendingReviewsByAgentParams{AgentID: agentID})
	if err != nil {
		return nil, wrapErr(err, "list")
	}
	result := make([]mcp.PendingHookReview, 0, len(rows))
	for _, row := range rows {
		result = append(result, pendingFromList(row))
	}
	return result, nil
}

// ResolvePendingReview marks a pending review as resolved with the given decision.
func (s *store) ResolvePendingReview(ctx context.Context, hookCallID, decision, reason, idempotencyKey, resolvedBy string) error {
	if _, err := s.q.CheckHookReviewIdempotency(ctx, sqlc.CheckHookReviewIdempotencyParams{
		HookCallID:     hookCallID,
		IdempotencyKey: idempotencyKey,
	}); err == nil {
		return nil
	} else if !platformdb.IsNotFound(err) {
		return wrapErr(err, "resolve.idempotency_check")
	}

	rows, err := s.q.ResolveHookPendingReview(ctx, sqlc.ResolveHookPendingReviewParams{
		HookCallID:     hookCallID,
		Decision:       decision,
		Reason:         reason,
		IdempotencyKey: idempotencyKey,
		ResolvedBy:     resolvedBy,
		ResolvedAt:     toMSPtr(time.Now().UTC()),
	})
	if err != nil {
		return wrapErr(err, "resolve")
	}
	if rows == 0 {
		return wrapErr(contract.ErrHookReviewNotFound, "resolve")
	}
	return nil
}

// GetResolvedReview returns the canonical decision metadata plus subscriber lease for a resolved review.
func (s *store) GetResolvedReview(ctx context.Context, hookCallID string) (string, time.Time, string, error) {
	row, err := s.q.GetHookResolvedReview(ctx, sqlc.GetHookResolvedReviewParams{HookCallID: hookCallID})
	if err != nil {
		if platformdb.IsNotFound(err) {
			err = contract.ErrHookReviewNotFound
		}
		return "", time.Time{}, "", wrapErr(err, "get_resolved")
	}
	return row.Decision, fromMSPtr(row.ResolvedAt), row.SubscriberLease, nil
}

// CancelPendingReviewsByLease marks all pending reviews for the given subscriber lease as cancelled.
func (s *store) CancelPendingReviewsByLease(ctx context.Context, subscriberLease string) (int, error) {
	rows, err := s.q.CancelHookPendingReviewsByLease(ctx, sqlc.CancelHookPendingReviewsByLeaseParams{
		SubscriberLease: subscriberLease,
		ResolvedAt:      toMSPtr(time.Now().UTC()),
	})
	if err != nil {
		return 0, wrapErr(err, "cancel_by_lease")
	}
	return int(rows), nil
}

// CancelPendingReviewsByAgent marks all pending reviews for the given agent as cancelled.
func (s *store) CancelPendingReviewsByAgent(ctx context.Context, agentID string) (int, error) {
	rows, err := s.q.CancelHookPendingReviewsByAgent(ctx, sqlc.CancelHookPendingReviewsByAgentParams{
		AgentID:    agentID,
		ResolvedAt: toMSPtr(time.Now().UTC()),
	})
	if err != nil {
		return 0, wrapErr(err, "cancel_by_agent")
	}
	return int(rows), nil
}

// CancelExpiredReviews transitions all expired pending reviews to their default action.
// Returns the number of reviews cancelled.
func (s *store) CancelExpiredReviews(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	rows, err := s.q.CancelExpiredHookReviews(ctx, sqlc.CancelExpiredHookReviewsParams{
		ResolvedAt: toMSPtr(now),
		DeadlineAt: toMS(now),
	})
	if err != nil {
		return 0, wrapErr(err, "cancel_expired")
	}
	return int(rows), nil
}

// RecoverOnStartup returns all reviews that are still pending (used at process start).
func (s *store) RecoverOnStartup(ctx context.Context) ([]mcp.PendingHookReview, error) {
	rows, err := s.q.RecoverHookPendingReviews(ctx)
	if err != nil {
		return nil, wrapErr(err, "recover")
	}
	result := make([]mcp.PendingHookReview, 0, len(rows))
	for _, row := range rows {
		result = append(result, pendingFromRecover(row))
	}
	return result, nil
}

func pendingFromGet(row sqlc.GetHookPendingReviewRow) mcp.PendingHookReview {
	return mcp.PendingHookReview{
		HookCallID:      row.HookCallID,
		Topic:           row.Topic,
		AgentID:         row.AgentID,
		SubscriberLease: row.SubscriberLease,
		DefaultAction:   row.DefaultAction,
		CreatedAt:       fromMS(row.CreatedAt),
		DeadlineAt:      fromMS(row.DeadlineAt),
	}
}

func pendingFromList(row sqlc.ListHookPendingReviewsByAgentRow) mcp.PendingHookReview {
	return mcp.PendingHookReview{
		HookCallID:      row.HookCallID,
		Topic:           row.Topic,
		AgentID:         row.AgentID,
		SubscriberLease: row.SubscriberLease,
		DefaultAction:   row.DefaultAction,
		CreatedAt:       fromMS(row.CreatedAt),
		DeadlineAt:      fromMS(row.DeadlineAt),
	}
}

func pendingFromRecover(row sqlc.RecoverHookPendingReviewsRow) mcp.PendingHookReview {
	return mcp.PendingHookReview{
		HookCallID:      row.HookCallID,
		Topic:           row.Topic,
		AgentID:         row.AgentID,
		SubscriberLease: row.SubscriberLease,
		DefaultAction:   row.DefaultAction,
		CreatedAt:       fromMS(row.CreatedAt),
		DeadlineAt:      fromMS(row.DeadlineAt),
	}
}

func toMS(t time.Time) int64 {
	return platformdb.Millis(t)
}

func toMSPtr(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	v := platformdb.Millis(t)
	return &v
}

func fromMS(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return platformdb.TimeFromMillis(ms)
}

func fromMSPtr(ms *int64) time.Time {
	if ms == nil {
		return time.Time{}
	}
	return fromMS(*ms)
}

func wrapErr(err error, op string) error {
	return platformdb.WrapStoreError(err, op, "hook_pending_review")
}
