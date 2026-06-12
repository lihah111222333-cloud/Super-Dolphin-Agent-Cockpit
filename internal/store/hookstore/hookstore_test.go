package hookstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

func TestSavePendingReview(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 3, 24, 10, 0, 0, 0, time.UTC)
	review := testPendingReview("call-save", "agent-save", "reject", createdAt, createdAt.Add(10*time.Minute))
	store, db := newTestStore()

	if err := store.SavePendingReview(context.Background(), review); err != nil {
		t.Fatalf("SavePendingReview() error = %v", err)
	}

	got := db.mustRecord(t, review.HookCallID)
	assertPendingReview(t, got.review, review)
	if got.status != "pending" {
		t.Fatalf("saved status = %q, want pending", got.status)
	}

	if err := store.SavePendingReview(context.Background(), review); err != nil {
		t.Fatalf("SavePendingReview() duplicate error = %v", err)
	}
	if len(db.records) != 1 {
		t.Fatalf("records = %d, want 1 after duplicate save", len(db.records))
	}
	if db.execCount("insert") != 2 {
		t.Fatalf("insert exec count = %d, want 2", db.execCount("insert"))
	}
}

func TestNewStore(t *testing.T) {
	t.Parallel()

	got := NewStore(nil)
	if got == nil {
		t.Fatal("NewStore() = nil, want non-nil store")
	}
	store, ok := got.(*store)
	if !ok {
		t.Fatalf("NewStore() type = %T, want *store", got)
	}
	if store.q != nil {
		t.Fatalf("NewStore().q = %#v, want nil querier", store.q)
	}
}

func TestGetPendingReview(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 24, 11, 0, 0, 0, time.UTC)
	pending := testPendingReview("call-pending", "agent-get", "reject", now, now.Add(30*time.Minute))
	resolved := testPendingReview("call-resolved", "agent-get", "approve", now.Add(time.Minute), now.Add(40*time.Minute))
	store, _ := newTestStore(
		testRecord{review: pending, status: "pending"},
		testRecord{review: resolved, status: "resolved", decision: "approve", idempotencyKey: "idem-resolved", resolvedAt: now.Add(2 * time.Minute)},
	)

	got, err := store.GetPendingReview(context.Background(), pending.HookCallID)
	if err != nil {
		t.Fatalf("GetPendingReview() error = %v", err)
	}
	assertPendingReview(t, got, pending)

	_, err = store.GetPendingReview(context.Background(), resolved.HookCallID)
	assertStoreError(t, err, "get", "hook_pending_review", contract.ErrHookReviewNotFound)
	if !errors.Is(err, contract.ErrHookReviewNotFound) {
		t.Fatalf("GetPendingReview() error = %v, want ErrHookReviewNotFound", err)
	}
}

func TestListPendingReviews(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC)
	older := testPendingReview("call-list-1", "agent-list", "reject", base, base.Add(10*time.Minute))
	newer := testPendingReview("call-list-2", "agent-list", "reject", base.Add(2*time.Minute), base.Add(20*time.Minute))
	resolved := testPendingReview("call-list-3", "agent-list", "approve", base.Add(time.Minute), base.Add(15*time.Minute))
	otherAgent := testPendingReview("call-list-4", "agent-other", "reject", base.Add(3*time.Minute), base.Add(25*time.Minute))
	store, _ := newTestStore(
		testRecord{review: newer, status: "pending"},
		testRecord{review: older, status: "pending"},
		testRecord{review: resolved, status: "resolved", decision: "approve", idempotencyKey: "idem-list", resolvedAt: base.Add(4 * time.Minute)},
		testRecord{review: otherAgent, status: "pending"},
	)

	got, err := store.ListPendingReviews(context.Background(), "agent-list")
	if err != nil {
		t.Fatalf("ListPendingReviews() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListPendingReviews() count = %d, want 2", len(got))
	}
	if got[0].HookCallID != older.HookCallID || got[1].HookCallID != newer.HookCallID {
		t.Fatalf("ListPendingReviews() order = [%s %s], want [%s %s]", got[0].HookCallID, got[1].HookCallID, older.HookCallID, newer.HookCallID)
	}
}

func testPendingReview(hookCallID, agentID, defaultAction string, createdAt, deadlineAt time.Time) mcp.PendingHookReview {
	return mcp.PendingHookReview{
		HookCallID:      hookCallID,
		Topic:           "topic/" + hookCallID,
		AgentID:         agentID,
		SubscriberLease: hookCallID + "/1",
		DefaultAction:   defaultAction,
		CreatedAt:       createdAt,
		DeadlineAt:      deadlineAt,
	}
}

func assertPendingReview(t *testing.T, got, want mcp.PendingHookReview) {
	t.Helper()

	if got != want {
		t.Fatalf("review = %+v, want %+v", got, want)
	}
}

func assertStoreError(t *testing.T, err error, operation, entity string, baseErr error) {
	t.Helper()

	if err == nil {
		t.Fatal("error = nil, want non-nil")
	}
	var storeErr *platformdb.StoreError
	if !errors.As(err, &storeErr) {
		t.Fatalf("error = %T %v, want *platformdb.StoreError", err, err)
	}
	if storeErr.Operation != operation || storeErr.Entity != entity {
		t.Fatalf("StoreError = %+v, want operation=%q entity=%q", storeErr, operation, entity)
	}
	if !errors.Is(err, baseErr) {
		t.Fatalf("error = %v, want wrapped base error %v", err, baseErr)
	}
	if !strings.Contains(err.Error(), operation+" "+entity+":") {
		t.Fatalf("error message = %q, want operation/entity prefix", err.Error())
	}
}
