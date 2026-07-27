package sqlc

import (
	"context"
	"testing"
)

func TestResolveHookPendingReviewSameKeyRetryIsAtomicAndPreservesFirstDecision(t *testing.T) {
	t.Parallel()

	db := openSQLCTestSQLiteDB(t)
	q := New(db)
	ctx := context.Background()
	if _, err := q.SaveHookPendingReview(ctx, SaveHookPendingReviewParams{
		HookCallID: "hook-1", Topic: "tool", AgentID: "agent-1",
		Payload: []byte(`{}`), DefaultAction: "reject", CreatedAt: 1, DeadlineAt: 100,
	}); err != nil {
		t.Fatalf("SaveHookPendingReview() error = %v", err)
	}
	firstResolvedAt := int64(10)
	first := ResolveHookPendingReviewParams{
		HookCallID: "hook-1", Decision: "approve", Reason: "first",
		IdempotencyKey: "idem-1", ResolvedBy: "reviewer-1", ResolvedAt: &firstResolvedAt,
	}
	rows, err := q.ResolveHookPendingReview(ctx, first)
	requireHookResolveRows(t, "first", rows, err, 1)
	retryResolvedAt := int64(20)
	retry := ResolveHookPendingReviewParams{
		HookCallID: "hook-1", Decision: "reject", Reason: "retry",
		IdempotencyKey: "idem-1", ResolvedBy: "reviewer-2", ResolvedAt: &retryResolvedAt,
	}
	rows, err = q.ResolveHookPendingReview(ctx, retry)
	requireHookResolveRows(t, "retry", rows, err, 1)
	var decision, reason, resolvedBy string
	var resolvedAt int64
	if err := db.QueryRow(`
		SELECT decision, reason, resolved_by, resolved_at
		FROM hook_pending_reviews WHERE hook_call_id = 'hook-1'
	`).Scan(&decision, &reason, &resolvedBy, &resolvedAt); err != nil {
		t.Fatalf("read resolved review: %v", err)
	}
	assertFirstHookResolvePayload(t, decision, reason, resolvedBy, resolvedAt, firstResolvedAt)
	retry.IdempotencyKey = "idem-other"
	rows, err = q.ResolveHookPendingReview(ctx, retry)
	requireHookResolveRows(t, "other key", rows, err, 0)
}

func requireHookResolveRows(t *testing.T, operation string, rows int64, err error, want int64) {
	t.Helper()
	if err != nil {
		t.Fatalf("ResolveHookPendingReview(%s) error = %v", operation, err)
	}
	if rows != want {
		t.Fatalf("ResolveHookPendingReview(%s) rows = %d, want %d", operation, rows, want)
	}
}

func assertFirstHookResolvePayload(
	t *testing.T,
	decision, reason, resolvedBy string,
	resolvedAt, firstResolvedAt int64,
) {
	t.Helper()
	if decision != "approve" || reason != "first" || resolvedBy != "reviewer-1" {
		t.Fatalf("resolved review payload = (%q,%q,%q), want first payload", decision, reason, resolvedBy)
	}
	if resolvedAt != firstResolvedAt {
		t.Fatalf("resolved_at = %d, want first timestamp %d", resolvedAt, firstResolvedAt)
	}
}
