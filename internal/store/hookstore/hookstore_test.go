package hookstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// Keep these compacted SQL strings aligned with hookstore.go so the test stub
// rejects stale query shape changes, including SELECT column count/order.
const (
	savePendingReviewSQL           = "INSERT INTO hook_pending_reviews ( hook_call_id, topic, agent_id, subscriber_lease, default_action, status, created_at, deadline_at ) VALUES ($1, $2, $3, $4, $5, 'pending', $6, $7) ON CONFLICT (hook_call_id) DO NOTHING"
	getPendingReviewSQL            = "SELECT hook_call_id, topic, agent_id, subscriber_lease, default_action, status, created_at, deadline_at FROM hook_pending_reviews WHERE hook_call_id = $1 AND status = 'pending'"
	listPendingReviewsSQL          = "SELECT hook_call_id, topic, agent_id, subscriber_lease, default_action, status, created_at, deadline_at FROM hook_pending_reviews WHERE agent_id = $1 AND status = 'pending' ORDER BY created_at ASC"
	resolveIdempotencySQL          = "SELECT 1 FROM hook_pending_reviews WHERE hook_call_id = $1 AND status = 'resolved' AND idempotency_key = $2"
	resolvePendingReviewSQL        = "UPDATE hook_pending_reviews SET status = 'resolved', decision = $2, reason = $3, idempotency_key = $4, resolved_by = $5, resolved_at = $6 WHERE hook_call_id = $1 AND status = 'pending'"
	cancelPendingReviewsByLeaseSQL = "UPDATE hook_pending_reviews SET status = 'cancelled', resolved_at = $2 WHERE subscriber_lease = $1 AND status = 'pending'"
	cancelPendingReviewsByAgentSQL = "UPDATE hook_pending_reviews SET status = 'cancelled', resolved_at = $2 WHERE agent_id = $1 AND status = 'pending'"
	cancelExpiredReviewsSQL        = "UPDATE hook_pending_reviews SET status = 'expired', decision = default_action, resolved_at = $1 WHERE status = 'pending' AND deadline_at <= $1"
	recoverOnStartupSQL            = "SELECT hook_call_id, topic, agent_id, subscriber_lease, default_action, status, created_at, deadline_at FROM hook_pending_reviews WHERE status = 'pending' ORDER BY deadline_at ASC"
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

	got := NewStore(sqlc.New(nil))
	if got == nil {
		t.Fatal("NewStore() = nil, want non-nil store")
	}
	store, ok := got.(*Store)
	if !ok {
		t.Fatalf("NewStore() type = %T, want *Store", got)
	}
	if store.db != nil {
		t.Fatalf("NewStore().db = %#v, want nil queryable for nil sqlc DB", store.db)
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

func pendingReviewFromArgs(args []any) (mcp.PendingHookReview, error) {
	hookCallID, ok := args[0].(string)
	if !ok {
		return mcp.PendingHookReview{}, fmt.Errorf("hook_call_id arg type = %T, want string", args[0])
	}
	topic, ok := args[1].(string)
	if !ok {
		return mcp.PendingHookReview{}, fmt.Errorf("topic arg type = %T, want string", args[1])
	}
	agentID, ok := args[2].(string)
	if !ok {
		return mcp.PendingHookReview{}, fmt.Errorf("agent_id arg type = %T, want string", args[2])
	}
	subscriberLease, ok := args[3].(string)
	if !ok {
		return mcp.PendingHookReview{}, fmt.Errorf("subscriber_lease arg type = %T, want string", args[3])
	}
	defaultAction, ok := args[4].(string)
	if !ok {
		return mcp.PendingHookReview{}, fmt.Errorf("default_action arg type = %T, want string", args[4])
	}
	createdAt, ok := args[5].(time.Time)
	if !ok {
		return mcp.PendingHookReview{}, fmt.Errorf("created_at arg type = %T, want time.Time", args[5])
	}
	deadlineAt, ok := args[6].(time.Time)
	if !ok {
		return mcp.PendingHookReview{}, fmt.Errorf("deadline_at arg type = %T, want time.Time", args[6])
	}
	return mcp.PendingHookReview{
		HookCallID:      hookCallID,
		Topic:           topic,
		AgentID:         agentID,
		SubscriberLease: subscriberLease,
		DefaultAction:   defaultAction,
		CreatedAt:       createdAt,
		DeadlineAt:      deadlineAt,
	}, nil
}

func rowsToValues(rows []*testRecord) [][]any {
	values := make([][]any, 0, len(rows))
	for _, row := range rows {
		values = append(values, reviewValues(row.review))
	}
	return values
}

func reviewValues(review mcp.PendingHookReview) []any {
	return []any{
		review.HookCallID,
		review.Topic,
		review.AgentID,
		review.SubscriberLease,
		review.DefaultAction,
		"pending",
		review.CreatedAt,
		review.DeadlineAt,
	}
}

func assignScanDest(dest []any, values []any) error {
	if len(dest) != len(values) {
		return fmt.Errorf("scan dest len = %d, want %d", len(dest), len(values))
	}
	for i := range dest {
		if err := assignValue(dest[i], values[i]); err != nil {
			return err
		}
	}
	return nil
}

func assignValue(dest, value any) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("scan dest = %T, want non-nil pointer", dest)
	}
	target := rv.Elem()
	if value == nil {
		target.Set(reflect.Zero(target.Type()))
		return nil
	}
	vv := reflect.ValueOf(value)
	if vv.Type().AssignableTo(target.Type()) {
		target.Set(vv)
		return nil
	}
	if vv.Type().ConvertibleTo(target.Type()) {
		target.Set(vv.Convert(target.Type()))
		return nil
	}
	return fmt.Errorf("cannot assign %T to %s", value, target.Type())
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

func compactSQL(query string) string {
	return strings.Join(strings.Fields(query), " ")
}
