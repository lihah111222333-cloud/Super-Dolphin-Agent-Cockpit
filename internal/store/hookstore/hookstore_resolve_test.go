package hookstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestResolvePendingReview(t *testing.T) {
	t.Parallel()
	t.Run("resolve pending", testResolvePendingReviewResolved)
	t.Run("idempotent when key matches existing resolved record", testResolvePendingReviewIdempotent)
	t.Run("non pending returns not found", testResolvePendingReviewNotFound)
}

func testResolvePendingReviewResolved(t *testing.T) {
	now := time.Date(2026, 3, 24, 13, 0, 0, 0, time.UTC)
	review := testPendingReview("call-resolve", "agent-resolve", "reject", now, now.Add(15*time.Minute))
	store, db := newTestStore(testRecord{review: review, status: "pending"})

	err := store.ResolvePendingReview(context.Background(), review.HookCallID, "approve", "looks good", "idem-1", "reviewer-store")
	if err != nil {
		t.Fatalf("ResolvePendingReview() error = %v", err)
	}

	got := db.mustRecord(t, review.HookCallID)
	if got.status != "resolved" {
		t.Fatalf("resolved status = %q, want resolved", got.status)
	}
	if got.decision != "approve" || got.reason != "looks good" || got.idempotencyKey != "idem-1" || got.resolvedBy != "reviewer-store" {
		t.Fatalf("resolved record = %+v", got)
	}
	if got.resolvedAt.IsZero() {
		t.Fatal("resolvedAt = zero, want non-zero")
	}
	if db.execCount("resolve") != 1 {
		t.Fatalf("resolve exec count = %d, want 1", db.execCount("resolve"))
	}
}

func testResolvePendingReviewIdempotent(t *testing.T) {
	now := time.Date(2026, 3, 24, 13, 30, 0, 0, time.UTC)
	review := testPendingReview("call-idempotent", "agent-resolve", "reject", now, now.Add(15*time.Minute))
	store, db := newTestStore(testRecord{
		review:         review,
		status:         "resolved",
		decision:       "approve",
		reason:         "already done",
		idempotencyKey: "idem-same",
		resolvedBy:     "reviewer-existing",
		resolvedAt:     now.Add(time.Minute),
	})

	err := store.ResolvePendingReview(context.Background(), review.HookCallID, "approve", "ignored", "idem-same", "reviewer-new")
	if err != nil {
		t.Fatalf("ResolvePendingReview() idempotent error = %v", err)
	}
	if db.execCount("resolve") != 0 {
		t.Fatalf("resolve exec count = %d, want 0", db.execCount("resolve"))
	}
	got := db.mustRecord(t, review.HookCallID)
	if got.reason != "already done" || got.resolvedBy != "reviewer-existing" {
		t.Fatalf("idempotent resolve changed record = %+v", got)
	}
}

func testResolvePendingReviewNotFound(t *testing.T) {
	now := time.Date(2026, 3, 24, 14, 0, 0, 0, time.UTC)
	review := testPendingReview("call-not-pending", "agent-resolve", "reject", now, now.Add(15*time.Minute))
	store, _ := newTestStore(testRecord{
		review:         review,
		status:         "cancelled",
		reason:         "shutdown",
		idempotencyKey: "idem-old",
		resolvedAt:     now.Add(time.Minute),
	})

	err := store.ResolvePendingReview(context.Background(), review.HookCallID, "approve", "retry", "idem-new", "reviewer-missing")
	assertStoreError(t, err, "resolve", "hook_pending_review", contract.ErrHookReviewNotFound)
	if !errors.Is(err, contract.ErrHookReviewNotFound) {
		t.Fatalf("ResolvePendingReview() error = %v, want ErrHookReviewNotFound", err)
	}
}

func TestCancelExpiredReviews(t *testing.T) {
	t.Parallel()
	t.Run("empty table returns zero", testCancelExpiredReviewsEmpty)
	t.Run("deadline boundary is inclusive", testCancelExpiredReviewsDeadlineBoundary)
	t.Run("marks only expired pending reviews", testCancelExpiredReviewsOnlyExpiredPending)
}

func testCancelExpiredReviewsEmpty(t *testing.T) {
	store, db := newTestStore()

	count, err := store.CancelExpiredReviews(context.Background())
	if err != nil {
		t.Fatalf("CancelExpiredReviews() error = %v", err)
	}
	if count != 0 {
		t.Fatalf("CancelExpiredReviews() count = %d, want 0", count)
	}
	if db.execCount("cancel_expired") != 1 {
		t.Fatalf("cancel_expired exec count = %d, want 1", db.execCount("cancel_expired"))
	}
}

func testCancelExpiredReviewsDeadlineBoundary(t *testing.T) {
	base := time.Date(2026, 3, 24, 14, 30, 0, 0, time.UTC)
	equalDeadline := testPendingReview("call-expired-equal", "agent-expired", "reject", base, base)
	futureDeadline := testPendingReview("call-expired-future", "agent-expired", "approve", base, base)
	store, db := newTestStore(
		testRecord{review: equalDeadline, status: "pending"},
		testRecord{review: futureDeadline, status: "pending"},
	)
	db.beforeCancelExpired = func(now time.Time) {
		db.records[equalDeadline.HookCallID].review.DeadlineAt = now
		db.records[futureDeadline.HookCallID].review.DeadlineAt = now.Add(time.Nanosecond)
	}

	count, err := store.CancelExpiredReviews(context.Background())
	if err != nil {
		t.Fatalf("CancelExpiredReviews() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("CancelExpiredReviews() count = %d, want 1", count)
	}

	equalRecord := db.mustRecord(t, equalDeadline.HookCallID)
	if equalRecord.status != "expired" || equalRecord.decision != equalDeadline.DefaultAction {
		t.Fatalf("equal deadline record = %+v", equalRecord)
	}
	if db.mustRecord(t, futureDeadline.HookCallID).status != "pending" {
		t.Fatalf("future deadline status = %q, want pending", db.mustRecord(t, futureDeadline.HookCallID).status)
	}
}

func testCancelExpiredReviewsOnlyExpiredPending(t *testing.T) {
	now := time.Now().UTC()
	expired := testPendingReview("call-expired", "agent-expired", "reject", now.Add(-2*time.Hour), now.Add(-time.Minute))
	active := testPendingReview("call-active", "agent-expired", "approve", now.Add(-time.Hour), now.Add(time.Hour))
	resolved := testPendingReview("call-resolved-expired", "agent-expired", "reject", now.Add(-3*time.Hour), now.Add(-2*time.Hour))
	store, db := newTestStore(
		testRecord{review: expired, status: "pending"},
		testRecord{review: active, status: "pending"},
		testRecord{review: resolved, status: "resolved", decision: "approve", idempotencyKey: "idem-expired", resolvedAt: now.Add(-30 * time.Minute)},
	)

	count, err := store.CancelExpiredReviews(context.Background())
	if err != nil {
		t.Fatalf("CancelExpiredReviews() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("CancelExpiredReviews() count = %d, want 1", count)
	}

	expiredRecord := db.mustRecord(t, expired.HookCallID)
	if expiredRecord.status != "expired" || expiredRecord.decision != expired.DefaultAction || expiredRecord.resolvedAt.IsZero() {
		t.Fatalf("expired record = %+v", expiredRecord)
	}
	if db.mustRecord(t, active.HookCallID).status != "pending" {
		t.Fatalf("active record status = %q, want pending", db.mustRecord(t, active.HookCallID).status)
	}
	if db.mustRecord(t, resolved.HookCallID).status != "resolved" {
		t.Fatalf("resolved record status = %q, want resolved", db.mustRecord(t, resolved.HookCallID).status)
	}
}

func TestCancelPendingReviewsByAgent(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 3, 24, 15, 0, 0, 0, time.UTC)
	agentPendingA := testPendingReview("call-cancel-1", "agent-cancel", "reject", base, base.Add(10*time.Minute))
	agentPendingB := testPendingReview("call-cancel-2", "agent-cancel", "approve", base.Add(time.Minute), base.Add(20*time.Minute))
	agentResolved := testPendingReview("call-cancel-3", "agent-cancel", "reject", base.Add(2*time.Minute), base.Add(30*time.Minute))
	otherAgent := testPendingReview("call-cancel-4", "agent-keep", "reject", base.Add(3*time.Minute), base.Add(40*time.Minute))
	store, db := newTestStore(
		testRecord{review: agentPendingA, status: "pending"},
		testRecord{review: agentPendingB, status: "pending"},
		testRecord{review: agentResolved, status: "resolved", decision: "approve", idempotencyKey: "idem-cancel", resolvedAt: base.Add(5 * time.Minute)},
		testRecord{review: otherAgent, status: "pending"},
	)

	count, err := store.CancelPendingReviewsByAgent(context.Background(), "agent-cancel")
	if err != nil {
		t.Fatalf("CancelPendingReviewsByAgent() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("CancelPendingReviewsByAgent() count = %d, want 2", count)
	}

	if db.mustRecord(t, agentPendingA.HookCallID).status != "cancelled" {
		t.Fatalf("first cancelled status = %q, want cancelled", db.mustRecord(t, agentPendingA.HookCallID).status)
	}
	if db.mustRecord(t, agentPendingB.HookCallID).status != "cancelled" {
		t.Fatalf("second cancelled status = %q, want cancelled", db.mustRecord(t, agentPendingB.HookCallID).status)
	}
	if db.mustRecord(t, agentResolved.HookCallID).status != "resolved" {
		t.Fatalf("resolved status = %q, want resolved", db.mustRecord(t, agentResolved.HookCallID).status)
	}
	if db.mustRecord(t, otherAgent.HookCallID).status != "pending" {
		t.Fatalf("other agent status = %q, want pending", db.mustRecord(t, otherAgent.HookCallID).status)
	}
}

func TestCancelPendingReviewsByLease(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 3, 24, 15, 30, 0, 0, time.UTC)
	sharedAgentLeaseA := testPendingReview("call-cancel-lease-1", "agent-cancel", "reject", base, base.Add(10*time.Minute))
	sharedAgentLeaseA.SubscriberLease = "lease-a/1"
	sharedAgentLeaseB := testPendingReview("call-cancel-lease-2", "agent-cancel", "approve", base.Add(time.Minute), base.Add(20*time.Minute))
	sharedAgentLeaseB.SubscriberLease = "lease-b/1"
	resolved := testPendingReview("call-cancel-lease-3", "agent-cancel", "reject", base.Add(2*time.Minute), base.Add(30*time.Minute))
	resolved.SubscriberLease = "lease-a/1"
	otherAgentSameLease := testPendingReview("call-cancel-lease-4", "agent-keep", "reject", base.Add(3*time.Minute), base.Add(40*time.Minute))
	otherAgentSameLease.SubscriberLease = "lease-a/1"
	store, db := newTestStore(
		testRecord{review: sharedAgentLeaseA, status: "pending"},
		testRecord{review: sharedAgentLeaseB, status: "pending"},
		testRecord{review: resolved, status: "resolved", decision: "approve", idempotencyKey: "idem-cancel-lease", resolvedAt: base.Add(5 * time.Minute)},
		testRecord{review: otherAgentSameLease, status: "pending"},
	)

	count, err := store.CancelPendingReviewsByLease(context.Background(), "lease-a/1")
	if err != nil {
		t.Fatalf("CancelPendingReviewsByLease() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("CancelPendingReviewsByLease() count = %d, want 2", count)
	}

	if db.mustRecord(t, sharedAgentLeaseA.HookCallID).status != "cancelled" {
		t.Fatalf("first lease status = %q, want cancelled", db.mustRecord(t, sharedAgentLeaseA.HookCallID).status)
	}
	if db.mustRecord(t, sharedAgentLeaseB.HookCallID).status != "pending" {
		t.Fatalf("other lease status = %q, want pending", db.mustRecord(t, sharedAgentLeaseB.HookCallID).status)
	}
	if db.mustRecord(t, resolved.HookCallID).status != "resolved" {
		t.Fatalf("resolved status = %q, want resolved", db.mustRecord(t, resolved.HookCallID).status)
	}
	if db.mustRecord(t, otherAgentSameLease.HookCallID).status != "cancelled" {
		t.Fatalf("same lease other agent status = %q, want cancelled", db.mustRecord(t, otherAgentSameLease.HookCallID).status)
	}
}

func TestRecoverOnStartup(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 3, 24, 16, 0, 0, 0, time.UTC)
	pendingSoon := testPendingReview("call-recover-1", "agent-recover", "reject", base, base.Add(5*time.Minute))
	pendingLater := testPendingReview("call-recover-2", "agent-recover", "approve", base.Add(time.Minute), base.Add(15*time.Minute))
	resolved := testPendingReview("call-recover-3", "agent-recover", "reject", base.Add(2*time.Minute), base.Add(25*time.Minute))
	cancelled := testPendingReview("call-recover-4", "agent-recover", "reject", base.Add(3*time.Minute), base.Add(35*time.Minute))
	store, _ := newTestStore(
		testRecord{review: pendingLater, status: "pending"},
		testRecord{review: resolved, status: "resolved", decision: "approve", idempotencyKey: "idem-recover", resolvedAt: base.Add(6 * time.Minute)},
		testRecord{review: pendingSoon, status: "pending"},
		testRecord{review: cancelled, status: "cancelled", resolvedAt: base.Add(7 * time.Minute)},
	)

	got, err := store.RecoverOnStartup(context.Background())
	if err != nil {
		t.Fatalf("RecoverOnStartup() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("RecoverOnStartup() count = %d, want 2", len(got))
	}
	if got[0].HookCallID != pendingSoon.HookCallID || got[1].HookCallID != pendingLater.HookCallID {
		t.Fatalf("RecoverOnStartup() order = [%s %s], want [%s %s]", got[0].HookCallID, got[1].HookCallID, pendingSoon.HookCallID, pendingLater.HookCallID)
	}
}
