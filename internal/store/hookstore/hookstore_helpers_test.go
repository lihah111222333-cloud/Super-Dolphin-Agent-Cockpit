package hookstore

import (
	"context"
	"database/sql"
	"sort"
	"testing"
	"time"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type testRecord struct {
	review         mcp.PendingHookReview
	status         string
	decision       string
	reason         string
	idempotencyKey string
	resolvedBy     string
	resolvedAt     time.Time
}

// hookStoreQuerierStub is an in-memory implementation of the hookstore querier
// interface. It models hook_pending_reviews state transitions so the store can
// be exercised without a real database, mirroring the generated sqlc semantics.
type hookStoreQuerierStub struct {
	records             map[string]*testRecord
	execOps             []string
	beforeCancelExpired func(now time.Time)
}

func newTestStore(records ...testRecord) (*store, *hookStoreQuerierStub) {
	db := &hookStoreQuerierStub{records: make(map[string]*testRecord, len(records))}
	for i := range records {
		record := records[i]
		recordCopy := record
		db.records[record.review.HookCallID] = &recordCopy
	}
	return newStoreForTest(db), db
}

func (db *hookStoreQuerierStub) SaveHookPendingReview(_ context.Context, arg sqlc.SaveHookPendingReviewParams) (int64, error) {
	db.execOps = append(db.execOps, "insert")
	if _, exists := db.records[arg.HookCallID]; exists {
		return 0, nil
	}
	db.records[arg.HookCallID] = &testRecord{
		review: mcp.PendingHookReview{
			HookCallID:      arg.HookCallID,
			Topic:           arg.Topic,
			AgentID:         arg.AgentID,
			ThreadID:        arg.ThreadID,
			TurnID:          arg.TurnID,
			SubscriberLease: arg.SubscriberLease,
			Payload:         arg.Payload,
			DefaultAction:   arg.DefaultAction,
			CreatedAt:       fromMS(arg.CreatedAt),
			DeadlineAt:      fromMS(arg.DeadlineAt),
		},
		status: "pending",
	}
	return 1, nil
}

func (db *hookStoreQuerierStub) GetHookPendingReviewForSave(_ context.Context, arg sqlc.GetHookPendingReviewForSaveParams) (sqlc.GetHookPendingReviewForSaveRow, error) {
	record, ok := db.records[arg.HookCallID]
	if !ok {
		return sqlc.GetHookPendingReviewForSaveRow{}, sql.ErrNoRows
	}
	return sqlc.GetHookPendingReviewForSaveRow{
		HookCallID:      record.review.HookCallID,
		Topic:           record.review.Topic,
		AgentID:         record.review.AgentID,
		ThreadID:        record.review.ThreadID,
		TurnID:          record.review.TurnID,
		SubscriberLease: record.review.SubscriberLease,
		Payload:         record.review.Payload,
		DefaultAction:   record.review.DefaultAction,
		Status:          record.status,
		CreatedAt:       toMS(record.review.CreatedAt),
		DeadlineAt:      toMS(record.review.DeadlineAt),
	}, nil
}

func (db *hookStoreQuerierStub) GetHookPendingReview(_ context.Context, arg sqlc.GetHookPendingReviewParams) (sqlc.GetHookPendingReviewRow, error) {
	record, ok := db.records[arg.HookCallID]
	if !ok || record.status != "pending" {
		return sqlc.GetHookPendingReviewRow{}, sql.ErrNoRows
	}
	return sqlc.GetHookPendingReviewRow{
		HookCallID:      record.review.HookCallID,
		Topic:           record.review.Topic,
		AgentID:         record.review.AgentID,
		ThreadID:        record.review.ThreadID,
		TurnID:          record.review.TurnID,
		SubscriberLease: record.review.SubscriberLease,
		Payload:         record.review.Payload,
		DefaultAction:   record.review.DefaultAction,
		Status:          "pending",
		CreatedAt:       toMS(record.review.CreatedAt),
		DeadlineAt:      toMS(record.review.DeadlineAt),
	}, nil
}

func (db *hookStoreQuerierStub) ListHookPendingReviewsByAgent(_ context.Context, arg sqlc.ListHookPendingReviewsByAgentParams) ([]sqlc.ListHookPendingReviewsByAgentRow, error) {
	rows := db.pendingReviewsByAgent(arg.AgentID)
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].review.CreatedAt.Before(rows[j].review.CreatedAt)
	})
	result := make([]sqlc.ListHookPendingReviewsByAgentRow, 0, len(rows))
	for _, record := range rows {
		result = append(result, sqlc.ListHookPendingReviewsByAgentRow{
			HookCallID:      record.review.HookCallID,
			Topic:           record.review.Topic,
			AgentID:         record.review.AgentID,
			ThreadID:        record.review.ThreadID,
			TurnID:          record.review.TurnID,
			SubscriberLease: record.review.SubscriberLease,
			Payload:         record.review.Payload,
			DefaultAction:   record.review.DefaultAction,
			Status:          "pending",
			CreatedAt:       toMS(record.review.CreatedAt),
			DeadlineAt:      toMS(record.review.DeadlineAt),
		})
	}
	return result, nil
}

func (db *hookStoreQuerierStub) CheckHookReviewIdempotency(_ context.Context, arg sqlc.CheckHookReviewIdempotencyParams) (int64, error) {
	record, ok := db.records[arg.HookCallID]
	if !ok || record.status != "resolved" || record.idempotencyKey != arg.IdempotencyKey {
		return 0, sql.ErrNoRows
	}
	return 1, nil
}

func (db *hookStoreQuerierStub) ResolveHookPendingReview(_ context.Context, arg sqlc.ResolveHookPendingReviewParams) (int64, error) {
	db.execOps = append(db.execOps, "resolve")
	record, exists := db.records[arg.HookCallID]
	if !exists || record.status != "pending" {
		return 0, nil
	}
	record.status = "resolved"
	record.decision = arg.Decision
	record.reason = arg.Reason
	record.idempotencyKey = arg.IdempotencyKey
	record.resolvedBy = arg.ResolvedBy
	record.resolvedAt = fromMSPtr(arg.ResolvedAt)
	return 1, nil
}

func (db *hookStoreQuerierStub) GetHookResolvedReview(_ context.Context, arg sqlc.GetHookResolvedReviewParams) (sqlc.GetHookResolvedReviewRow, error) {
	record, ok := db.records[arg.HookCallID]
	if !ok || record.status != "resolved" {
		return sqlc.GetHookResolvedReviewRow{}, sql.ErrNoRows
	}
	return sqlc.GetHookResolvedReviewRow{
		Decision:        record.decision,
		ResolvedAt:      toMSPtr(record.resolvedAt),
		SubscriberLease: record.review.SubscriberLease,
	}, nil
}

func (db *hookStoreQuerierStub) CancelHookPendingReviewsByLease(_ context.Context, arg sqlc.CancelHookPendingReviewsByLeaseParams) (int64, error) {
	db.execOps = append(db.execOps, "cancel_by_lease")
	resolvedAt := fromMSPtr(arg.ResolvedAt)
	count := int64(0)
	for _, record := range db.records {
		if record.review.SubscriberLease == arg.SubscriberLease && record.status == "pending" {
			record.status = "cancelled"
			record.resolvedAt = resolvedAt
			count++
		}
	}
	return count, nil
}

func (db *hookStoreQuerierStub) CancelHookPendingReviewsByAgent(_ context.Context, arg sqlc.CancelHookPendingReviewsByAgentParams) (int64, error) {
	db.execOps = append(db.execOps, "cancel_by_agent")
	resolvedAt := fromMSPtr(arg.ResolvedAt)
	count := int64(0)
	for _, record := range db.records {
		if record.review.AgentID == arg.AgentID && record.status == "pending" {
			record.status = "cancelled"
			record.resolvedAt = resolvedAt
			count++
		}
	}
	return count, nil
}

func (db *hookStoreQuerierStub) CancelExpiredHookReviews(_ context.Context, arg sqlc.CancelExpiredHookReviewsParams) (int64, error) {
	db.execOps = append(db.execOps, "cancel_expired")
	now := fromMS(arg.DeadlineAt)
	if db.beforeCancelExpired != nil {
		db.beforeCancelExpired(now)
	}
	count := int64(0)
	for _, record := range db.records {
		if record.status == "pending" && !record.review.DeadlineAt.After(now) {
			record.status = "expired"
			record.decision = record.review.DefaultAction
			record.resolvedAt = now
			count++
		}
	}
	return count, nil
}

func (db *hookStoreQuerierStub) RecoverHookPendingReviews(_ context.Context) ([]sqlc.RecoverHookPendingReviewsRow, error) {
	rows := db.pendingReviews()
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].review.DeadlineAt.Before(rows[j].review.DeadlineAt)
	})
	result := make([]sqlc.RecoverHookPendingReviewsRow, 0, len(rows))
	for _, record := range rows {
		result = append(result, sqlc.RecoverHookPendingReviewsRow{
			HookCallID:      record.review.HookCallID,
			Topic:           record.review.Topic,
			AgentID:         record.review.AgentID,
			ThreadID:        record.review.ThreadID,
			TurnID:          record.review.TurnID,
			SubscriberLease: record.review.SubscriberLease,
			Payload:         record.review.Payload,
			DefaultAction:   record.review.DefaultAction,
			Status:          "pending",
			CreatedAt:       toMS(record.review.CreatedAt),
			DeadlineAt:      toMS(record.review.DeadlineAt),
		})
	}
	return result, nil
}

func (db *hookStoreQuerierStub) pendingReviewsByAgent(agentID string) []*testRecord {
	var rows []*testRecord
	for _, record := range db.records {
		if record.review.AgentID == agentID && record.status == "pending" {
			rows = append(rows, record)
		}
	}
	return rows
}

func (db *hookStoreQuerierStub) pendingReviews() []*testRecord {
	var rows []*testRecord
	for _, record := range db.records {
		if record.status == "pending" {
			rows = append(rows, record)
		}
	}
	return rows
}

func (db *hookStoreQuerierStub) execCount(op string) int {
	count := 0
	for _, got := range db.execOps {
		if got == op {
			count++
		}
	}
	return count
}

func (db *hookStoreQuerierStub) mustRecord(t *testing.T, hookCallID string) testRecord {
	t.Helper()

	record, ok := db.records[hookCallID]
	if !ok {
		t.Fatalf("record %q not found", hookCallID)
	}
	return *record
}

// Ensure the stub stays aligned with the production not-found mapping.
var _ = platformdb.IsNotFound
