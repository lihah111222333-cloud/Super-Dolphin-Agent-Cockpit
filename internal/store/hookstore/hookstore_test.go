package hookstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Keep these compacted SQL strings aligned with hookstore.go so the test stub
// rejects stale query shape changes, including SELECT column count/order.
const (
	savePendingReviewSQL           = "INSERT INTO hook_pending_reviews ( hook_call_id, topic, agent_id, default_action, status, created_at, deadline_at ) VALUES ($1, $2, $3, $4, 'pending', $5, $6) ON CONFLICT (hook_call_id) DO NOTHING"
	getPendingReviewSQL            = "SELECT hook_call_id, topic, agent_id, default_action, status, created_at, deadline_at FROM hook_pending_reviews WHERE hook_call_id = $1 AND status = 'pending'"
	listPendingReviewsSQL          = "SELECT hook_call_id, topic, agent_id, default_action, status, created_at, deadline_at FROM hook_pending_reviews WHERE agent_id = $1 AND status = 'pending' ORDER BY created_at ASC"
	resolveIdempotencySQL          = "SELECT 1 FROM hook_pending_reviews WHERE hook_call_id = $1 AND status = 'resolved' AND idempotency_key = $2"
	resolvePendingReviewSQL        = "UPDATE hook_pending_reviews SET status = 'resolved', decision = $2, reason = $3, idempotency_key = $4, resolved_at = $5 WHERE hook_call_id = $1 AND status = 'pending'"
	cancelPendingReviewsByAgentSQL = "UPDATE hook_pending_reviews SET status = 'cancelled', resolved_at = $2 WHERE agent_id = $1 AND status = 'pending'"
	cancelExpiredReviewsSQL        = "UPDATE hook_pending_reviews SET status = 'expired', decision = default_action, resolved_at = $1 WHERE status = 'pending' AND deadline_at <= $1"
	recoverOnStartupSQL            = "SELECT hook_call_id, topic, agent_id, default_action, status, created_at, deadline_at FROM hook_pending_reviews WHERE status = 'pending' ORDER BY deadline_at ASC"
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
	assertStoreError(t, err, "get", "hook_pending_review", pgx.ErrNoRows)
	if !errors.Is(err, platformdb.ErrNotFound) {
		t.Fatalf("GetPendingReview() error = %v, want ErrNotFound", err)
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

func TestResolvePendingReview(t *testing.T) {
	t.Parallel()

	t.Run("resolve pending", func(t *testing.T) {
		now := time.Date(2026, 3, 24, 13, 0, 0, 0, time.UTC)
		review := testPendingReview("call-resolve", "agent-resolve", "reject", now, now.Add(15*time.Minute))
		store, db := newTestStore(testRecord{review: review, status: "pending"})

		err := store.ResolvePendingReview(context.Background(), review.HookCallID, "approve", "looks good", "idem-1")
		if err != nil {
			t.Fatalf("ResolvePendingReview() error = %v", err)
		}

		got := db.mustRecord(t, review.HookCallID)
		if got.status != "resolved" {
			t.Fatalf("resolved status = %q, want resolved", got.status)
		}
		if got.decision != "approve" || got.reason != "looks good" || got.idempotencyKey != "idem-1" {
			t.Fatalf("resolved record = %+v", got)
		}
		if got.resolvedAt.IsZero() {
			t.Fatal("resolvedAt = zero, want non-zero")
		}
		if db.execCount("resolve") != 1 {
			t.Fatalf("resolve exec count = %d, want 1", db.execCount("resolve"))
		}
	})

	t.Run("idempotent when key matches existing resolved record", func(t *testing.T) {
		now := time.Date(2026, 3, 24, 13, 30, 0, 0, time.UTC)
		review := testPendingReview("call-idempotent", "agent-resolve", "reject", now, now.Add(15*time.Minute))
		store, db := newTestStore(testRecord{
			review:         review,
			status:         "resolved",
			decision:       "approve",
			reason:         "already done",
			idempotencyKey: "idem-same",
			resolvedAt:     now.Add(time.Minute),
		})

		err := store.ResolvePendingReview(context.Background(), review.HookCallID, "approve", "ignored", "idem-same")
		if err != nil {
			t.Fatalf("ResolvePendingReview() idempotent error = %v", err)
		}
		if db.execCount("resolve") != 0 {
			t.Fatalf("resolve exec count = %d, want 0", db.execCount("resolve"))
		}
		got := db.mustRecord(t, review.HookCallID)
		if got.reason != "already done" {
			t.Fatalf("idempotent resolve changed record = %+v", got)
		}
	})

	t.Run("non pending returns not found", func(t *testing.T) {
		now := time.Date(2026, 3, 24, 14, 0, 0, 0, time.UTC)
		review := testPendingReview("call-not-pending", "agent-resolve", "reject", now, now.Add(15*time.Minute))
		store, _ := newTestStore(testRecord{
			review:         review,
			status:         "cancelled",
			reason:         "shutdown",
			idempotencyKey: "idem-old",
			resolvedAt:     now.Add(time.Minute),
		})

		err := store.ResolvePendingReview(context.Background(), review.HookCallID, "approve", "retry", "idem-new")
		assertStoreError(t, err, "resolve", "hook_pending_review", platformdb.ErrNotFound)
		if !errors.Is(err, platformdb.ErrNotFound) {
			t.Fatalf("ResolvePendingReview() error = %v, want ErrNotFound", err)
		}
	})
}

func TestCancelExpiredReviews(t *testing.T) {
	t.Parallel()

	t.Run("empty table returns zero", func(t *testing.T) {
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
	})

	t.Run("deadline boundary is inclusive", func(t *testing.T) {
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
	})

	t.Run("marks only expired pending reviews", func(t *testing.T) {
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
	})
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

type testRecord struct {
	review         mcp.PendingHookReview
	status         string
	decision       string
	reason         string
	idempotencyKey string
	resolvedAt     time.Time
}

type hookStoreDBStub struct {
	records             map[string]*testRecord
	execOps             []string
	beforeCancelExpired func(now time.Time)
}

func newTestStore(records ...testRecord) (*Store, *hookStoreDBStub) {
	db := &hookStoreDBStub{records: make(map[string]*testRecord, len(records))}
	for i := range records {
		record := records[i]
		recordCopy := record
		db.records[record.review.HookCallID] = &recordCopy
	}
	return &Store{db: db}, db
}

func (db *hookStoreDBStub) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	switch compactSQL(query) {
	case savePendingReviewSQL:
		db.execOps = append(db.execOps, "insert")
		return db.execSave(args)
	case resolvePendingReviewSQL:
		db.execOps = append(db.execOps, "resolve")
		return db.execResolve(args)
	case cancelPendingReviewsByAgentSQL:
		db.execOps = append(db.execOps, "cancel_by_agent")
		return db.execCancelByAgent(args)
	case cancelExpiredReviewsSQL:
		db.execOps = append(db.execOps, "cancel_expired")
		return db.execCancelExpired(args)
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec query: %s", compactSQL(query))
	}
}

func (db *hookStoreDBStub) Query(_ context.Context, query string, args ...any) (pgx.Rows, error) {
	switch compactSQL(query) {
	case listPendingReviewsSQL:
		agentID, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("agent_id arg type = %T, want string", args[0])
		}
		rows := db.pendingReviewsByAgent(agentID)
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].review.CreatedAt.Before(rows[j].review.CreatedAt)
		})
		return newHookRows(rowsToValues(rows)), nil
	case recoverOnStartupSQL:
		rows := db.pendingReviews()
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].review.DeadlineAt.Before(rows[j].review.DeadlineAt)
		})
		return newHookRows(rowsToValues(rows)), nil
	default:
		return nil, fmt.Errorf("unexpected Query query: %s", compactSQL(query))
	}
}

func (db *hookStoreDBStub) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	switch compactSQL(query) {
	case getPendingReviewSQL:
		hookCallID, ok := args[0].(string)
		if !ok {
			return hookRowStub{err: fmt.Errorf("hook_call_id arg type = %T, want string", args[0])}
		}
		record, ok := db.records[hookCallID]
		if !ok || record.status != "pending" {
			return hookRowStub{err: pgx.ErrNoRows}
		}
		return hookRowStub{values: reviewValues(record.review)}
	case resolveIdempotencySQL:
		hookCallID, ok := args[0].(string)
		if !ok {
			return hookRowStub{err: fmt.Errorf("hook_call_id arg type = %T, want string", args[0])}
		}
		idempotencyKey, ok := args[1].(string)
		if !ok {
			return hookRowStub{err: fmt.Errorf("idempotency_key arg type = %T, want string", args[1])}
		}
		record, ok := db.records[hookCallID]
		if !ok || record.status != "resolved" || record.idempotencyKey != idempotencyKey {
			return hookRowStub{err: pgx.ErrNoRows}
		}
		return hookRowStub{values: []any{1}}
	default:
		return hookRowStub{err: fmt.Errorf("unexpected QueryRow query: %s", compactSQL(query))}
	}
}

func (db *hookStoreDBStub) execSave(args []any) (pgconn.CommandTag, error) {
	if len(args) != 6 {
		return pgconn.CommandTag{}, fmt.Errorf("save args = %d, want 6", len(args))
	}
	review, err := pendingReviewFromArgs(args)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	if _, exists := db.records[review.HookCallID]; exists {
		return pgconn.NewCommandTag("INSERT 0 0"), nil
	}
	db.records[review.HookCallID] = &testRecord{review: review, status: "pending"}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (db *hookStoreDBStub) execResolve(args []any) (pgconn.CommandTag, error) {
	if len(args) != 5 {
		return pgconn.CommandTag{}, fmt.Errorf("resolve args = %d, want 5", len(args))
	}
	hookCallID, ok := args[0].(string)
	if !ok {
		return pgconn.CommandTag{}, fmt.Errorf("hook_call_id arg type = %T, want string", args[0])
	}
	decision, ok := args[1].(string)
	if !ok {
		return pgconn.CommandTag{}, fmt.Errorf("decision arg type = %T, want string", args[1])
	}
	reason, ok := args[2].(string)
	if !ok {
		return pgconn.CommandTag{}, fmt.Errorf("reason arg type = %T, want string", args[2])
	}
	idempotencyKey, ok := args[3].(string)
	if !ok {
		return pgconn.CommandTag{}, fmt.Errorf("idempotency_key arg type = %T, want string", args[3])
	}
	resolvedAt, ok := args[4].(time.Time)
	if !ok {
		return pgconn.CommandTag{}, fmt.Errorf("resolved_at arg type = %T, want time.Time", args[4])
	}
	record, exists := db.records[hookCallID]
	if !exists || record.status != "pending" {
		return pgconn.NewCommandTag("UPDATE 0"), nil
	}
	record.status = "resolved"
	record.decision = decision
	record.reason = reason
	record.idempotencyKey = idempotencyKey
	record.resolvedAt = resolvedAt
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (db *hookStoreDBStub) execCancelByAgent(args []any) (pgconn.CommandTag, error) {
	if len(args) != 2 {
		return pgconn.CommandTag{}, fmt.Errorf("cancel_by_agent args = %d, want 2", len(args))
	}
	agentID, ok := args[0].(string)
	if !ok {
		return pgconn.CommandTag{}, fmt.Errorf("agent_id arg type = %T, want string", args[0])
	}
	resolvedAt, ok := args[1].(time.Time)
	if !ok {
		return pgconn.CommandTag{}, fmt.Errorf("resolved_at arg type = %T, want time.Time", args[1])
	}
	count := 0
	for _, record := range db.records {
		if record.review.AgentID == agentID && record.status == "pending" {
			record.status = "cancelled"
			record.resolvedAt = resolvedAt
			count++
		}
	}
	return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", count)), nil
}

func (db *hookStoreDBStub) execCancelExpired(args []any) (pgconn.CommandTag, error) {
	if len(args) != 1 {
		return pgconn.CommandTag{}, fmt.Errorf("cancel_expired args = %d, want 1", len(args))
	}
	now, ok := args[0].(time.Time)
	if !ok {
		return pgconn.CommandTag{}, fmt.Errorf("deadline arg type = %T, want time.Time", args[0])
	}
	if db.beforeCancelExpired != nil {
		db.beforeCancelExpired(now)
	}
	count := 0
	for _, record := range db.records {
		if record.status == "pending" && !record.review.DeadlineAt.After(now) {
			record.status = "expired"
			record.decision = record.review.DefaultAction
			record.resolvedAt = now
			count++
		}
	}
	return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", count)), nil
}

func (db *hookStoreDBStub) pendingReviewsByAgent(agentID string) []*testRecord {
	var rows []*testRecord
	for _, record := range db.records {
		if record.review.AgentID == agentID && record.status == "pending" {
			rows = append(rows, record)
		}
	}
	return rows
}

func (db *hookStoreDBStub) pendingReviews() []*testRecord {
	var rows []*testRecord
	for _, record := range db.records {
		if record.status == "pending" {
			rows = append(rows, record)
		}
	}
	return rows
}

func (db *hookStoreDBStub) execCount(op string) int {
	count := 0
	for _, got := range db.execOps {
		if got == op {
			count++
		}
	}
	return count
}

func (db *hookStoreDBStub) mustRecord(t *testing.T, hookCallID string) testRecord {
	t.Helper()

	record, ok := db.records[hookCallID]
	if !ok {
		t.Fatalf("record %q not found", hookCallID)
	}
	return *record
}

type hookRowStub struct {
	values []any
	err    error
}

func (r hookRowStub) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return assignScanDest(dest, r.values)
}

type hookRowsStub struct {
	values [][]any
	index  int
	err    error
}

func newHookRows(values [][]any) *hookRowsStub {
	return &hookRowsStub{values: values}
}

func (r *hookRowsStub) Close() {}

func (r *hookRowsStub) Err() error { return r.err }

func (r *hookRowsStub) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (r *hookRowsStub) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (r *hookRowsStub) Next() bool {
	if r.index >= len(r.values) {
		return false
	}
	r.index++
	return true
}

func (r *hookRowsStub) Scan(dest ...any) error {
	if r.index == 0 || r.index > len(r.values) {
		return errors.New("hookRowsStub: invalid cursor")
	}
	return assignScanDest(dest, r.values[r.index-1])
}

func (r *hookRowsStub) Values() ([]any, error) {
	if r.index == 0 || r.index > len(r.values) {
		return nil, errors.New("hookRowsStub: invalid cursor")
	}
	return append([]any(nil), r.values[r.index-1]...), nil
}

func (r *hookRowsStub) RawValues() [][]byte { return nil }

func (r *hookRowsStub) Conn() *pgx.Conn { return nil }

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
	defaultAction, ok := args[3].(string)
	if !ok {
		return mcp.PendingHookReview{}, fmt.Errorf("default_action arg type = %T, want string", args[3])
	}
	createdAt, ok := args[4].(time.Time)
	if !ok {
		return mcp.PendingHookReview{}, fmt.Errorf("created_at arg type = %T, want time.Time", args[4])
	}
	deadlineAt, ok := args[5].(time.Time)
	if !ok {
		return mcp.PendingHookReview{}, fmt.Errorf("deadline_at arg type = %T, want time.Time", args[5])
	}
	return mcp.PendingHookReview{
		HookCallID:    hookCallID,
		Topic:         topic,
		AgentID:       agentID,
		DefaultAction: defaultAction,
		CreatedAt:     createdAt,
		DeadlineAt:    deadlineAt,
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
		HookCallID:    hookCallID,
		Topic:         "topic/" + hookCallID,
		AgentID:       agentID,
		DefaultAction: defaultAction,
		CreatedAt:     createdAt,
		DeadlineAt:    deadlineAt,
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
