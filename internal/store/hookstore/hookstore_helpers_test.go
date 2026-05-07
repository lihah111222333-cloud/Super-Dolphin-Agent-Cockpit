package hookstore

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
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

type hookStoreDBStub struct {
	records             map[string]*testRecord
	execOps             []string
	beforeCancelExpired func(now time.Time)
}

func newTestStore(records ...testRecord) (*store, *hookStoreDBStub) {
	db := &hookStoreDBStub{records: make(map[string]*testRecord, len(records))}
	for i := range records {
		record := records[i]
		recordCopy := record
		db.records[record.review.HookCallID] = &recordCopy
	}
	return newStoreForTest(sqlc.New(db)), db
}

func (db *hookStoreDBStub) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	switch compactSQL(query) {
	case savePendingReviewSQL:
		db.execOps = append(db.execOps, "insert")
		return db.execSave(args)
	case resolvePendingReviewSQL:
		db.execOps = append(db.execOps, "resolve")
		return db.execResolve(args)
	case cancelPendingReviewsByLeaseSQL:
		db.execOps = append(db.execOps, "cancel_by_lease")
		return db.execCancelByLease(args)
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
	if len(args) != 7 {
		return pgconn.CommandTag{}, fmt.Errorf("save args = %d, want 7", len(args))
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
	if len(args) != 6 {
		return pgconn.CommandTag{}, fmt.Errorf("resolve args = %d, want 6", len(args))
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
	resolvedBy, ok := args[4].(string)
	if !ok {
		return pgconn.CommandTag{}, fmt.Errorf("resolved_by arg type = %T, want string", args[4])
	}
	resolvedAt, err := timeArg(args[5], "resolved_at")
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	record, exists := db.records[hookCallID]
	if !exists || record.status != "pending" {
		return pgconn.NewCommandTag("UPDATE 0"), nil
	}
	record.status = "resolved"
	record.decision = decision
	record.reason = reason
	record.idempotencyKey = idempotencyKey
	record.resolvedBy = resolvedBy
	record.resolvedAt = resolvedAt
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (db *hookStoreDBStub) execCancelByLease(args []any) (pgconn.CommandTag, error) {
	if len(args) != 2 {
		return pgconn.CommandTag{}, fmt.Errorf("cancel_by_lease args = %d, want 2", len(args))
	}
	subscriberLease, ok := args[0].(string)
	if !ok {
		return pgconn.CommandTag{}, fmt.Errorf("subscriber_lease arg type = %T, want string", args[0])
	}
	resolvedAt, err := timeArg(args[1], "resolved_at")
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	count := 0
	for _, record := range db.records {
		if record.review.SubscriberLease == subscriberLease && record.status == "pending" {
			record.status = "cancelled"
			record.resolvedAt = resolvedAt
			count++
		}
	}
	return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", count)), nil
}

func (db *hookStoreDBStub) execCancelByAgent(args []any) (pgconn.CommandTag, error) {
	if len(args) != 2 {
		return pgconn.CommandTag{}, fmt.Errorf("cancel_by_agent args = %d, want 2", len(args))
	}
	agentID, ok := args[0].(string)
	if !ok {
		return pgconn.CommandTag{}, fmt.Errorf("agent_id arg type = %T, want string", args[0])
	}
	resolvedAt, err := timeArg(args[1], "resolved_at")
	if err != nil {
		return pgconn.CommandTag{}, err
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
	now, err := timeArg(args[0], "deadline")
	if err != nil {
		return pgconn.CommandTag{}, err
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

func timeArg(value any, name string) (time.Time, error) {
	switch t := value.(type) {
	case time.Time:
		return t, nil
	case pgtype.Timestamptz:
		return fromTS(t), nil
	default:
		return time.Time{}, fmt.Errorf("%s arg type = %T, want pgtype.Timestamptz", name, value)
	}
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
