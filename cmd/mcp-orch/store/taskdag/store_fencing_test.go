//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

const testLeaseInterval = "00:00:30"

func TestClaimDueWakeupsSkipsAlreadyClaimedWakeups(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	db.wakeups[7] = newPendingWakeup(now, 7)

	first, err := store.ClaimDueWakeups(context.Background(), ClaimDueWakeupsInput{
		ClaimedBy:     "worker-a",
		LeaseInterval: testLeaseInterval,
		Limit:         1,
	})
	if err != nil {
		t.Fatalf("ClaimDueWakeups() error = %v", err)
	}
	second, err := store.ClaimDueWakeups(context.Background(), ClaimDueWakeupsInput{
		ClaimedBy:     "worker-b",
		LeaseInterval: testLeaseInterval,
		Limit:         1,
	})
	if err != nil {
		t.Fatalf("ClaimDueWakeups() second error = %v", err)
	}
	if len(first) != 1 || len(second) != 0 {
		t.Fatalf("claim lens = %d/%d, want 1/0", len(first), len(second))
	}
	if first[0].ClaimedBy != "worker-a" {
		t.Fatalf("ClaimedBy = %q, want worker-a", first[0].ClaimedBy)
	}
}

func TestReclaimStaleDispatchingWakeupsAllowsFreshClaimAndBlocksStaleCommit(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	db.wakeups[7] = newPendingWakeup(now, 7)

	claimed := claimOneDueWakeup(t, store, "worker-a")

	db.advance(31 * time.Second)
	count := markWakeupSentForClaim(t, store, claimed)
	if count != 0 {
		t.Fatalf("MarkWakeupSent() stale count = %d, want 0", count)
	}

	reclaimed, err := store.ReclaimStaleDispatchingWakeups(context.Background())
	if err != nil {
		t.Fatalf("ReclaimStaleDispatchingWakeups() error = %v", err)
	}
	if reclaimed != 1 {
		t.Fatalf("reclaimed = %d, want 1", reclaimed)
	}

	reclaimedClaim := claimOneDueWakeup(t, store, "worker-b")
	if reclaimedClaim.ClaimedBy != "worker-b" || reclaimedClaim.AttemptCount != 2 {
		t.Fatalf("reclaimed wakeup = %+v", reclaimedClaim)
	}
}

func TestReclaimStaleDispatchingWakeupsSkipsRunningActiveWakeup(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runID := db.runs["run-1"].ID
	db.wakeups[7] = newDispatchingWakeup(now, 7, "worker-a", -time.Second)
	db.nodes[dagRunNodeKey("dag-1", "node-1", runID)] = newRunningNode(now, 7)

	reclaimed, err := store.ReclaimStaleDispatchingWakeups(context.Background())
	if err != nil {
		t.Fatalf("ReclaimStaleDispatchingWakeups() error = %v", err)
	}
	if reclaimed != 0 {
		t.Fatalf("reclaimed = %d, want 0 while node is running with active_wakeup_id", reclaimed)
	}
	if got := db.wakeups[7].Status; got != "dispatching" {
		t.Fatalf("wakeup status = %q, want dispatching", got)
	}
}

func TestCompleteNodeRejectsStaleWakeupAttempt(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runID := db.runs["run-1"].ID
	wakeup := newDispatchingWakeup(now, 7, "worker-a", 30*time.Second)
	wakeup.AttemptCount = 2
	db.wakeups[7] = wakeup
	db.nodes[dagRunNodeKey("dag-1", "node-1", runID)] = newRunningNode(now, 7)

	_, err := store.CompleteNode(context.Background(), CompleteNodeInput{
		DagKey:        "dag-1",
		NodeKey:       "node-1",
		RunID:         runID,
		Status:        "done",
		Result:        json.RawMessage(`{}`),
		WakeupID:      7,
		WakeupAttempt: 1,
	})
	if err == nil {
		t.Fatal("CompleteNode() stale attempt error = nil, want fence rejection")
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "node-1", runID)].Status; got != "running" {
		t.Fatalf("node status = %q, want running after stale completion", got)
	}
}

func claimOneDueWakeup(t *testing.T, store Store, worker string) Wakeup {
	t.Helper()
	claimed, err := store.ClaimDueWakeups(context.Background(), ClaimDueWakeupsInput{
		ClaimedBy:     worker,
		LeaseInterval: testLeaseInterval,
		Limit:         1,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimDueWakeups(%s) = %v, %d rows", worker, err, len(claimed))
	}
	return claimed[0]
}

func markWakeupSentForClaim(t *testing.T, store Store, claimed Wakeup) int64 {
	t.Helper()
	count, err := store.MarkWakeupSent(context.Background(), MarkWakeupSentInput{
		ID:             claimed.ID,
		ClaimedAt:      *claimed.ClaimedAt,
		ClaimedBy:      claimed.ClaimedBy,
		LeaseExpiresAt: *claimed.LeaseExpiresAt,
	})
	if err != nil {
		t.Fatalf("MarkWakeupSent() stale error = %v", err)
	}
	return count
}

func TestWakeupTransitionSQLUsesFullClaimFence(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		run    func(Store) (int64, error)
		wants  []string
		argLen int
	}{
		{
			name: "mark_sent",
			run: func(store Store) (int64, error) {
				return store.MarkWakeupSent(context.Background(), MarkWakeupSentInput{
					ID:             7,
					ClaimedAt:      now,
					ClaimedBy:      "worker-a",
					LeaseExpiresAt: now.Add(30 * time.Second),
				})
			},
			wants:  []string{"claimed_at = $2", "claimed_by = $3", "lease_expires_at = $4", "lease_expires_at >= NOW()"},
			argLen: 4,
		},
		{
			name: "retry",
			run: func(store Store) (int64, error) {
				return store.RetryWakeup(context.Background(), RetryWakeupInput{
					ID:             7,
					RetryInterval:  "00:02:00",
					LastError:      "busy",
					ClaimedAt:      now,
					ClaimedBy:      "worker-a",
					LeaseExpiresAt: now.Add(30 * time.Second),
				})
			},
			wants:  []string{"claimed_at = $4", "claimed_by = $5", "lease_expires_at = $6", "lease_expires_at >= NOW()"},
			argLen: 6,
		},
		{
			name: "fail",
			run: func(store Store) (int64, error) {
				return store.FailWakeup(context.Background(), FailWakeupInput{
					ID:             7,
					LastError:      "fatal",
					ClaimedAt:      now,
					ClaimedBy:      "worker-a",
					LeaseExpiresAt: now.Add(30 * time.Second),
				})
			},
			wants:  []string{"claimed_at = $3", "claimed_by = $4", "lease_expires_at = $5", "lease_expires_at >= NOW()"},
			argLen: 5,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			db := &captureExecTaskDAGDB{}
			store := NewStore(db)

			count, err := tt.run(store)
			if err != nil {
				t.Fatalf("%s error = %v", tt.name, err)
			}
			if count != 1 {
				t.Fatalf("%s count = %d, want 1", tt.name, count)
			}
			if len(db.args) != tt.argLen {
				t.Fatalf("%s args len = %d, want %d", tt.name, len(db.args), tt.argLen)
			}
			for _, want := range tt.wants {
				if !strings.Contains(db.sql, want) {
					t.Fatalf("%s SQL missing %q: %s", tt.name, want, db.sql)
				}
			}
		})
	}
}

func TestWakeupTransitionsApplyClaimFenceValues(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		run    func(Store) (int64, error)
		assert func(*testing.T, sqlc.TaskDagWakeup, time.Time)
	}{
		{
			name: "mark_sent",
			run: func(store Store) (int64, error) {
				return store.MarkWakeupSent(context.Background(), MarkWakeupSentInput{
					ID:             7,
					ClaimedAt:      now,
					ClaimedBy:      "worker-a",
					LeaseExpiresAt: now.Add(30 * time.Second),
				})
			},
			assert: assertWakeupMarkedSent,
		},
		{
			name: "retry",
			run: func(store Store) (int64, error) {
				return store.RetryWakeup(context.Background(), RetryWakeupInput{
					ID:             7,
					RetryInterval:  "00:02:00",
					LastError:      "busy",
					ClaimedAt:      now,
					ClaimedBy:      "worker-a",
					LeaseExpiresAt: now.Add(30 * time.Second),
				})
			},
			assert: assertWakeupRetried,
		},
		{
			name: "fail",
			run: func(store Store) (int64, error) {
				return store.FailWakeup(context.Background(), FailWakeupInput{
					ID:             7,
					LastError:      "fatal",
					ClaimedAt:      now,
					ClaimedBy:      "worker-a",
					LeaseExpiresAt: now.Add(30 * time.Second),
				})
			},
			assert: assertWakeupFailed,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			store, db, _ := newTaskDAGTestStore()
			db.wakeups[7] = newDispatchingWakeup(now, 7, "worker-a", 30*time.Second)

			count, err := tt.run(store)
			if err != nil {
				t.Fatalf("%s error = %v", tt.name, err)
			}
			if count != 1 {
				t.Fatalf("%s count = %d, want 1", tt.name, count)
			}
			tt.assert(t, db.wakeups[7], now)
		})
	}
}

func assertWakeupMarkedSent(t *testing.T, row sqlc.TaskDagWakeup, now time.Time) {
	t.Helper()
	if row.Status != "sent" {
		t.Fatalf("status = %q, want sent", row.Status)
	}
	if !sameTimestamp(row.SentAt, timestamptzValue(now)) {
		t.Fatalf("sent_at = %#v, want %v", row.SentAt, now)
	}
}

func assertWakeupRetried(t *testing.T, row sqlc.TaskDagWakeup, now time.Time) {
	t.Helper()
	if row.Status != "pending" {
		t.Fatalf("status = %q, want pending", row.Status)
	}
	if row.LastError != "busy" {
		t.Fatalf("last_error = %q, want busy", row.LastError)
	}
	assertClaimFenceCleared(t, row)
	if !sameTimestamp(row.NextRetryAt, timestamptzValue(now.Add(2*time.Minute))) {
		t.Fatalf("next_retry_at = %#v, want %v", row.NextRetryAt, now.Add(2*time.Minute))
	}
}

func assertWakeupFailed(t *testing.T, row sqlc.TaskDagWakeup, _ time.Time) {
	t.Helper()
	if row.Status != "failed" {
		t.Fatalf("status = %q, want failed", row.Status)
	}
	if row.LastError != "fatal" {
		t.Fatalf("last_error = %q, want fatal", row.LastError)
	}
	assertClaimFenceCleared(t, row)
}

func assertClaimFenceCleared(t *testing.T, row sqlc.TaskDagWakeup) {
	t.Helper()
	if row.ClaimedBy != "" || row.ClaimedAt.Valid || row.LeaseExpiresAt.Valid {
		t.Fatalf("claim fence not cleared: %+v", row)
	}
}

func TestBindRunningNodeTurnBindsWakeupAndClearsActiveWakeupOnCompletion(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runID := db.runs["run-1"].ID
	db.wakeups[7] = newSentWakeup(now, 7)
	db.nodes[dagRunNodeKey("dag-1", "node-1", runID)] = newRunningNode(now, 7)

	node, err := store.BindRunningNodeTurn(context.Background(), BindRunningNodeTurnInput{
		DagKey:   "dag-1",
		NodeKey:  "node-1",
		RunID:    runID,
		WakeupID: 7,
		TurnID:   "turn-1",
	})
	if err != nil {
		t.Fatalf("BindRunningNodeTurn() error = %v", err)
	}
	if node.ActiveTurnID == nil || *node.ActiveTurnID != "turn-1" {
		t.Fatalf("bound node = %+v", node)
	}
	if !db.wakeups[7].BoundTurnID.Valid || db.wakeups[7].BoundTurnID.String != "turn-1" {
		t.Fatalf("bound wakeup = %+v", db.wakeups[7])
	}

	completed, err := store.CompleteNode(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "node-1",
		RunID:   runID,
		Status:  "done",
		Result:  json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("CompleteNode() error = %v", err)
	}
	if completed.ActiveWakeupID != nil || completed.ActiveTurnID != nil {
		t.Fatalf("completed node = %+v, want active bindings cleared", completed)
	}
}

func TestBindRunningNodeTurnRejectsSecondWorkerForSameWakeup(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runID := db.runs["run-1"].ID
	db.wakeups[7] = newSentWakeup(now, 7)
	db.nodes[dagRunNodeKey("dag-1", "node-1", runID)] = newRunningNode(now, 7)

	if _, err := store.BindRunningNodeTurn(context.Background(), BindRunningNodeTurnInput{
		DagKey:   "dag-1",
		NodeKey:  "node-1",
		RunID:    runID,
		WakeupID: 7,
		TurnID:   "turn-1",
	}); err != nil {
		t.Fatalf("first BindRunningNodeTurn() error = %v", err)
	}
	if _, err := store.BindRunningNodeTurn(context.Background(), BindRunningNodeTurnInput{
		DagKey:   "dag-1",
		NodeKey:  "node-1",
		RunID:    runID,
		WakeupID: 7,
		TurnID:   "turn-2",
	}); err == nil {
		t.Fatal("second BindRunningNodeTurn() error = nil, want conflict")
	}
	if got := db.wakeups[7].BoundTurnID.String; got != "turn-1" {
		t.Fatalf("BoundTurnID = %q, want turn-1", got)
	}
}

func newTaskDAGTestStore() (Store, *fakeTaskDAGDB, time.Time) {
	now := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)
	db := newFakeTaskDAGDB(now)
	seedRun(db, "dag-1", "run-1")
	return NewStore(db), db, now
}

func newPendingWakeup(now time.Time, id int64) sqlc.TaskDagWakeup {
	runID := int64(1)
	return sqlc.TaskDagWakeup{
		ID:            id,
		DagKey:        "dag-1",
		NodeKey:       "node-1",
		RunID:         sqlc.Int8ValuePtr(&runID),
		WakeupKind:    "start",
		TargetAgentID: "agent-1",
		Status:        "pending",
		NextRetryAt:   timestamptzValue(now.Add(-time.Second)),
		CreatedAt:     timestamptzValue(now),
		UpdatedAt:     timestamptzValue(now),
	}
}

func newDispatchingWakeup(now time.Time, id int64, claimedBy string, lease time.Duration) sqlc.TaskDagWakeup {
	row := newPendingWakeup(now, id)
	row.Status = "dispatching"
	row.AttemptCount = 1
	row.ClaimedAt = timestamptzValue(now)
	row.ClaimedBy = claimedBy
	row.LeaseExpiresAt = timestamptzValue(now.Add(lease))
	return row
}

func newSentWakeup(now time.Time, id int64) sqlc.TaskDagWakeup {
	row := newPendingWakeup(now, id)
	row.Status = "sent"
	row.AttemptCount = 1
	row.SentAt = timestamptzValue(now)
	return row
}

func newRunningNode(now time.Time, wakeupID int64) sqlc.TaskDagNode {
	runID := int64(1)
	return sqlc.TaskDagNode{
		ID:             1,
		DagKey:         "dag-1",
		NodeKey:        "node-1",
		RunID:          sqlc.Int8ValuePtr(&runID),
		Title:          "node-1",
		Status:         "running",
		DependsOn:      []byte(`[]`),
		Config:         []byte(`{}`),
		Result:         []byte(`{}`),
		CreatedAt:      timestamptzValue(now),
		UpdatedAt:      timestamptzValue(now),
		ActiveWakeupID: sqlc.Int8ValuePtr(&wakeupID),
	}
}

type captureExecTaskDAGDB struct {
	sql  string
	args []any
}

func (db *captureExecTaskDAGDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	db.sql = sql
	db.args = append([]any(nil), args...)
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (*captureExecTaskDAGDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected Query call")
}

func (*captureExecTaskDAGDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return captureExecTaskDAGRow{err: fmt.Errorf("unexpected QueryRow call")}
}

type captureExecTaskDAGRow struct{ err error }

func (r captureExecTaskDAGRow) Scan(...any) error { return r.err }
