package hookstore

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
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

func TestSavePendingReviewRejectsConflictingDuplicate(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 3, 24, 10, 0, 0, 0, time.UTC)
	review := testPendingReview("call-save-conflict", "agent-save", "reject", createdAt, createdAt.Add(10*time.Minute))
	store, _ := newTestStore()

	if err := store.SavePendingReview(context.Background(), review); err != nil {
		t.Fatalf("SavePendingReview() error = %v", err)
	}
	changed := review
	changed.Topic = "topic/changed"
	changed.DefaultAction = "approve"
	changed.SubscriberLease = "other-lease"
	changed.Payload = []byte(`{"changed":true}`)

	err := store.SavePendingReview(context.Background(), changed)
	if !errors.Is(err, contract.ErrHookReviewConflict) {
		t.Fatalf("SavePendingReview() conflict error = %v, want ErrHookReviewConflict", err)
	}
}

func TestNewStore(t *testing.T) {
	t.Parallel()

	got := NewStore(nil)
	if got == nil {
		t.Fatal("NewStore() = nil, want non-nil store")
	}
	store, ok := got.(*pagedStore)
	if !ok {
		t.Fatalf("NewStore() type = %T, want *pagedStore", got)
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

// TestListPendingReviewsCapsLimit 固定 hookstore 下沉查询前必须裁剪过大分页 limit。
func TestListPendingReviewsCapsLimit(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 3, 24, 13, 0, 0, 0, time.UTC)
	store, db := newTestStore(
		testRecord{review: testPendingReview("call-cap-1", "agent-cap", "reject", base, base.Add(10*time.Minute)), status: "pending"},
		testRecord{review: testPendingReview("call-cap-2", "agent-cap", "reject", base.Add(time.Minute), base.Add(20*time.Minute)), status: "pending"},
	)

	_, err := store.ListPendingReviewsPage(context.Background(), contract.HookPendingReviewPageParams{
		AgentID: "agent-cap",
		Limit:   999,
	})

	if err != nil {
		t.Fatalf("ListPendingReviewsPage() error = %v", err)
	}
	if len(db.listPageLimits) != 1 || db.listPageLimits[0] != 500 {
		t.Fatalf("ListPendingReviewsPage() limits = %v, want [500]", db.listPageLimits)
	}
}

func TestListPendingReviewsPageContinuesAfterZeroTimestampCursor(t *testing.T) {
	t.Parallel()

	zero := time.Time{}
	deadline := time.Unix(60, 0).UTC()
	store, _ := newTestStore(
		testRecord{review: testPendingReview("call-zero-a", "agent-zero", "reject", zero, deadline), status: "pending"},
		testRecord{review: testPendingReview("call-zero-b", "agent-zero", "reject", zero, deadline), status: "pending"},
	)

	first, err := store.ListPendingReviewsPage(context.Background(), contract.HookPendingReviewPageParams{
		AgentID: "agent-zero",
		Limit:   1,
	})
	if err != nil {
		t.Fatalf("ListPendingReviewsPage(first) error = %v", err)
	}
	if !first.HasMore || len(first.Reviews) != 1 || first.Reviews[0].HookCallID != "call-zero-a" ||
		!first.NextCursorCreatedAt.IsZero() || first.NextCursorHookCallID != "call-zero-a" {
		t.Fatalf("first page = %+v, want zero-time cursor at call-zero-a", first)
	}

	second, err := store.ListPendingReviewsPage(context.Background(), contract.HookPendingReviewPageParams{
		AgentID:          "agent-zero",
		Limit:            1,
		CursorCreatedAt:  first.NextCursorCreatedAt,
		CursorHookCallID: first.NextCursorHookCallID,
	})
	if err != nil {
		t.Fatalf("ListPendingReviewsPage(second) error = %v", err)
	}
	if len(second.Reviews) != 1 || second.Reviews[0].HookCallID != "call-zero-b" {
		t.Fatalf("second page = %+v, want call-zero-b without restarting", second)
	}
}

func TestListPendingReviewsPageRejectsTimestampWithoutCursorID(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore()
	_, err := store.ListPendingReviewsPage(context.Background(), contract.HookPendingReviewPageParams{
		AgentID:         "agent-cursor",
		Limit:           1,
		CursorCreatedAt: time.Unix(1, 0).UTC(),
	})
	if err == nil || !strings.Contains(err.Error(), "cursor requires hook call ID") {
		t.Fatalf("ListPendingReviewsPage() error = %v, want incomplete cursor rejection", err)
	}
}

func testPendingReview(hookCallID, agentID, defaultAction string, createdAt, deadlineAt time.Time) mcp.PendingHookReview {
	return mcp.PendingHookReview{
		HookCallID:      hookCallID,
		Topic:           "topic/" + hookCallID,
		AgentID:         agentID,
		ThreadID:        "thread/" + hookCallID,
		TurnID:          "turn/" + hookCallID,
		SubscriberLease: hookCallID + "/1",
		Payload:         []byte(`{"hook_call_id":"` + hookCallID + `","source":"test"}`),
		DefaultAction:   defaultAction,
		CreatedAt:       createdAt,
		DeadlineAt:      deadlineAt,
	}
}

func assertPendingReview(t *testing.T, got, want mcp.PendingHookReview) {
	t.Helper()

	assertReviewString(t, "HookCallID", got.HookCallID, want.HookCallID)
	assertReviewString(t, "Topic", got.Topic, want.Topic)
	assertReviewString(t, "AgentID", got.AgentID, want.AgentID)
	assertReviewString(t, "ThreadID", got.ThreadID, want.ThreadID)
	assertReviewString(t, "TurnID", got.TurnID, want.TurnID)
	assertReviewString(t, "SubscriberLease", got.SubscriberLease, want.SubscriberLease)
	assertReviewString(t, "DefaultAction", got.DefaultAction, want.DefaultAction)
	assertReviewTime(t, "CreatedAt", got.CreatedAt, want.CreatedAt)
	assertReviewTime(t, "DeadlineAt", got.DeadlineAt, want.DeadlineAt)
	assertReviewPayload(t, got.Payload, want.Payload)
}

func assertReviewString(t *testing.T, name, got, want string) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}

func assertReviewInt(t *testing.T, name string, got, want int) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %d, want %d", name, got, want)
	}
}

func assertReviewTime(t *testing.T, name string, got, want time.Time) {
	t.Helper()

	if !got.Equal(want) {
		t.Fatalf("%s = %s, want %s", name, got, want)
	}
}

func assertReviewPayload(t *testing.T, got, want []byte) {
	t.Helper()

	if !bytes.Equal(got, want) {
		t.Fatalf("Payload = %s, want %s", string(got), string(want))
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
