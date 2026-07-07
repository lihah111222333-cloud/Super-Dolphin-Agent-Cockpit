package hookstore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	_ "modernc.org/sqlite"
)

func TestHookStoreSQLiteSavePendingReviewIsIdempotent(t *testing.T) {
	t.Parallel()

	store, db := newHookStoreSQLiteStore(t)
	base := time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC)
	review := testPendingReview("call-save-idem", "agent-save", "reject", base, base.Add(time.Hour))
	if err := store.SavePendingReview(context.Background(), review); err != nil {
		t.Fatalf("SavePendingReview() first error = %v", err)
	}
	if err := store.SavePendingReview(context.Background(), review); err != nil {
		t.Fatalf("SavePendingReview() idempotent error = %v", err)
	}
	changed := review
	changed.Topic = "topic/changed"
	changed.DefaultAction = "approve"
	changed.SubscriberLease = "changed-lease"
	changed.Payload = []byte(`{"changed":true}`)
	if err := store.SavePendingReview(context.Background(), changed); !errors.Is(err, contract.ErrHookReviewConflict) {
		t.Fatalf("SavePendingReview() conflict error = %v, want ErrHookReviewConflict", err)
	}

	assertSQLitePendingReviewUnchanged(t, db, review)
}

func TestHookStoreSQLiteRoundTripsContextFields(t *testing.T) {
	t.Parallel()

	store, _ := newHookStoreSQLiteStore(t)
	base := time.Date(2026, 3, 24, 12, 30, 0, 0, time.UTC)
	review := testPendingReview("call-roundtrip", "agent-roundtrip", "reject", base, base.Add(time.Hour))
	if err := store.SavePendingReview(context.Background(), review); err != nil {
		t.Fatalf("SavePendingReview() error = %v", err)
	}

	got, err := store.GetPendingReview(context.Background(), review.HookCallID)
	if err != nil {
		t.Fatalf("GetPendingReview() error = %v", err)
	}
	assertPendingReview(t, got, review)

	listed, err := store.ListPendingReviews(context.Background(), review.AgentID)
	if err != nil {
		t.Fatalf("ListPendingReviews() error = %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListPendingReviews() len = %d, want 1", len(listed))
	}
	assertPendingReview(t, listed[0], review)

	recovered, err := store.RecoverOnStartup(context.Background())
	if err != nil {
		t.Fatalf("RecoverOnStartup() error = %v", err)
	}
	if len(recovered) != 1 {
		t.Fatalf("RecoverOnStartup() len = %d, want 1", len(recovered))
	}
	assertPendingReview(t, recovered[0], review)
}

func TestHookStoreSQLiteResolveIdempotencyKey(t *testing.T) {
	t.Parallel()

	store, db := newHookStoreSQLiteStore(t)
	base := time.Date(2026, 3, 24, 13, 0, 0, 0, time.UTC)
	review := testPendingReview("call-resolve-sqlite", "agent-resolve", "reject", base, base.Add(time.Hour))
	if err := store.SavePendingReview(context.Background(), review); err != nil {
		t.Fatalf("SavePendingReview() error = %v", err)
	}

	if err := store.ResolvePendingReview(context.Background(), review.HookCallID, "approve", "first", "idem-1", "reviewer-1"); err != nil {
		t.Fatalf("ResolvePendingReview() first error = %v", err)
	}
	if err := store.ResolvePendingReview(context.Background(), review.HookCallID, "approve", "ignored", "idem-1", "reviewer-2"); err != nil {
		t.Fatalf("ResolvePendingReview() idempotent error = %v", err)
	}
	assertHookResolvedMetadata(t, db, review.HookCallID, "approve", "first", "idem-1", "reviewer-1")

	err := store.ResolvePendingReview(context.Background(), review.HookCallID, "reject", "stale", "idem-2", "reviewer-3")
	if !errors.Is(err, contract.ErrHookReviewNotFound) {
		t.Fatalf("ResolvePendingReview() stale key error = %v, want ErrHookReviewNotFound", err)
	}
	assertHookResolvedMetadata(t, db, review.HookCallID, "approve", "first", "idem-1", "reviewer-1")
}

func TestHookStoreSQLiteTransitionsSingleWinner(t *testing.T) {
	t.Parallel()

	store, db := newHookStoreSQLiteStore(t)
	base := time.Now().UTC().Add(-2 * time.Hour)
	resolveFirst := testPendingReview("call-resolve-before-expire", "agent-race", "reject", base, base.Add(time.Minute))
	cancelFirst := testPendingReview("call-cancel-before-resolve", "agent-race", "reject", base, base.Add(3*time.Hour))
	cancelFirst.SubscriberLease = "lease-cancel-first"
	for _, review := range []mcp.PendingHookReview{resolveFirst, cancelFirst} {
		if err := store.SavePendingReview(context.Background(), review); err != nil {
			t.Fatalf("SavePendingReview(%s) error = %v", review.HookCallID, err)
		}
	}

	if err := store.ResolvePendingReview(context.Background(), resolveFirst.HookCallID, "approve", "won", "idem-race", "reviewer-race"); err != nil {
		t.Fatalf("ResolvePendingReview() before expire error = %v", err)
	}
	expired, err := store.CancelExpiredReviews(context.Background())
	if err != nil {
		t.Fatalf("CancelExpiredReviews() after resolve error = %v", err)
	}
	if expired != 0 {
		t.Fatalf("CancelExpiredReviews() rows = %d, want 0 after resolve won", expired)
	}
	assertHookReviewStatus(t, db, resolveFirst.HookCallID, "resolved", "approve")

	cancelled, err := store.CancelPendingReviewsByLease(context.Background(), cancelFirst.SubscriberLease)
	if err != nil {
		t.Fatalf("CancelPendingReviewsByLease() error = %v", err)
	}
	if cancelled != 1 {
		t.Fatalf("CancelPendingReviewsByLease() rows = %d, want 1", cancelled)
	}
	err = store.ResolvePendingReview(context.Background(), cancelFirst.HookCallID, "approve", "too late", "idem-cancelled", "reviewer-race")
	if !errors.Is(err, contract.ErrHookReviewNotFound) {
		t.Fatalf("ResolvePendingReview() after cancel error = %v, want ErrHookReviewNotFound", err)
	}
	assertHookReviewStatus(t, db, cancelFirst.HookCallID, "cancelled", "")
}

func TestHookStoreSQLiteCancelRecoverExpirePrecise(t *testing.T) {
	t.Parallel()

	store, db := newHookStoreSQLiteStore(t)
	base := time.Now().UTC()
	cancelled := testPendingReview("call-cancel-sqlite", "agent-precise", "reject", base.Add(-time.Hour), base.Add(time.Hour))
	cancelled.SubscriberLease = "lease-stop"
	expired := testPendingReview("call-expire-sqlite", "agent-precise", "approve", base.Add(-2*time.Hour), base.Add(-time.Hour))
	future := testPendingReview("call-future-sqlite", "agent-precise", "reject", base.Add(-30*time.Minute), base.Add(2*time.Hour))
	for _, review := range []mcp.PendingHookReview{cancelled, expired, future} {
		if err := store.SavePendingReview(context.Background(), review); err != nil {
			t.Fatalf("SavePendingReview(%s) error = %v", review.HookCallID, err)
		}
	}

	cancelCount, err := store.CancelPendingReviewsByLease(context.Background(), "lease-stop")
	if err != nil {
		t.Fatalf("CancelPendingReviewsByLease() error = %v", err)
	}
	if cancelCount != 1 {
		t.Fatalf("CancelPendingReviewsByLease() rows = %d, want 1", cancelCount)
	}
	expireCount, err := store.CancelExpiredReviews(context.Background())
	if err != nil {
		t.Fatalf("CancelExpiredReviews() error = %v", err)
	}
	if expireCount != 1 {
		t.Fatalf("CancelExpiredReviews() rows = %d, want 1", expireCount)
	}
	assertHookReviewStatus(t, db, cancelled.HookCallID, "cancelled", "")
	assertHookReviewStatus(t, db, expired.HookCallID, "expired", expired.DefaultAction)

	recovered, err := store.RecoverOnStartup(context.Background())
	if err != nil {
		t.Fatalf("RecoverOnStartup() error = %v", err)
	}
	if len(recovered) != 1 || recovered[0].HookCallID != future.HookCallID {
		t.Fatalf("RecoverOnStartup() = %+v, want only %s", recovered, future.HookCallID)
	}
}

func newHookStoreSQLiteStore(t *testing.T) (*pagedStore, *sql.DB) {
	t.Helper()

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "hookstore.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	})
	if _, err := db.Exec(`
CREATE TABLE hook_pending_reviews (
	hook_call_id TEXT PRIMARY KEY,
	topic TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	thread_id TEXT NOT NULL DEFAULT '',
	turn_id TEXT NOT NULL DEFAULT '',
	subscriber_lease TEXT NOT NULL DEFAULT '',
	payload TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(payload)),
	decision TEXT NOT NULL DEFAULT '',
	reason TEXT NOT NULL DEFAULT '',
	default_action TEXT NOT NULL DEFAULT 'reject',
	status TEXT NOT NULL DEFAULT 'pending',
	created_at INTEGER NOT NULL,
	deadline_at INTEGER NOT NULL,
	resolved_at INTEGER,
	idempotency_key TEXT NOT NULL DEFAULT '',
	resolved_by TEXT NOT NULL DEFAULT ''
);
`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	q := sqlc.New(db)
	return &pagedStore{store: &store{q: q}, pages: q}, db
}

func assertHookReviewStatus(t *testing.T, db *sql.DB, hookCallID, wantStatus, wantDecision string) {
	t.Helper()

	var status, decision string
	if err := db.QueryRow("SELECT status, decision FROM hook_pending_reviews WHERE hook_call_id = ?", hookCallID).Scan(&status, &decision); err != nil {
		t.Fatalf("query hook review status: %v", err)
	}
	if status != wantStatus || decision != wantDecision {
		t.Fatalf("hook review status/decision = %q/%q, want %q/%q", status, decision, wantStatus, wantDecision)
	}
}

func assertHookResolvedMetadata(t *testing.T, db *sql.DB, hookCallID, wantDecision, wantReason, wantIdempotencyKey, wantResolvedBy string) {
	t.Helper()

	var decision, reason, idempotencyKey, resolvedBy string
	if err := db.QueryRow("SELECT decision, reason, idempotency_key, resolved_by FROM hook_pending_reviews WHERE hook_call_id = ?", hookCallID).Scan(&decision, &reason, &idempotencyKey, &resolvedBy); err != nil {
		t.Fatalf("query hook review metadata: %v", err)
	}
	if decision != wantDecision || reason != wantReason || idempotencyKey != wantIdempotencyKey || resolvedBy != wantResolvedBy {
		t.Fatalf("hook review metadata = %q/%q/%q/%q, want %q/%q/%q/%q", decision, reason, idempotencyKey, resolvedBy, wantDecision, wantReason, wantIdempotencyKey, wantResolvedBy)
	}
}

func assertSQLitePendingReviewUnchanged(t *testing.T, db *sql.DB, review mcp.PendingHookReview) {
	t.Helper()

	var count int
	var topic, threadID, turnID, payload, defaultAction string
	if err := db.QueryRow("SELECT COUNT(*), topic, thread_id, turn_id, payload, default_action FROM hook_pending_reviews WHERE hook_call_id = ?", review.HookCallID).Scan(&count, &topic, &threadID, &turnID, &payload, &defaultAction); err != nil {
		t.Fatalf("query saved review: %v", err)
	}
	assertReviewInt(t, "saved count", count, 1)
	assertReviewString(t, "saved topic", topic, review.Topic)
	assertReviewString(t, "saved thread_id", threadID, review.ThreadID)
	assertReviewString(t, "saved turn_id", turnID, review.TurnID)
	assertReviewString(t, "saved payload", payload, string(review.Payload))
	assertReviewString(t, "saved default_action", defaultAction, review.DefaultAction)
}
