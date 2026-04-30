package taskdag

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeTaskDAGDB struct {
	mu        sync.Mutex
	now       time.Time
	wakeups   map[int64]sqlc.TaskDagWakeup
	nodes     map[string]sqlc.TaskDagNode
	wakeupSeq int64
}

func newFakeTaskDAGDB(now time.Time) *fakeTaskDAGDB {
	return &fakeTaskDAGDB{
		now:     now.UTC(),
		wakeups: make(map[int64]sqlc.TaskDagWakeup),
		nodes:   make(map[string]sqlc.TaskDagNode),
	}
}

func (db *fakeTaskDAGDB) advance(delta time.Duration) { db.now = db.now.Add(delta) }

func (db *fakeTaskDAGDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	switch {
	case strings.Contains(sql, "BindTaskDagWakeupTurn"):
		return updateTag(db.bindWakeupTurn(args...))
	case strings.Contains(sql, "MarkTaskDagWakeupSent"):
		return updateTag(db.markWakeupSent(args...))
	case strings.Contains(sql, "RetryTaskDagWakeup"):
		return updateTag(db.retryWakeup(args...))
	case strings.Contains(sql, "FailTaskDagWakeup"):
		return updateTag(db.failWakeup(args...))
	case strings.Contains(sql, "ReclaimStaleDispatchingTaskDagWakeups"):
		return updateTag(db.reclaimStaleWakeups())
	case strings.Contains(sql, "EnqueueTaskDagWakeup"):
		return updateTag(db.enqueueWakeup(args...))
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec call: %s", firstLine(sql))
	}
}

func (db *fakeTaskDAGDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	switch {
	case strings.Contains(sql, "ClaimDueTaskDagWakeups"):
		rows, err := db.claimDueWakeups(args...)
		if err != nil {
			return nil, err
		}
		return &stubTaskDAGRows{rows: rows}, nil
	case strings.Contains(sql, "ListTaskDagNodes"):
		rows, err := db.listTaskDagNodes(args...)
		if err != nil {
			return nil, err
		}
		return &stubTaskDAGRows{rows: rows}, nil
	default:
		return nil, fmt.Errorf("unexpected Query call: %s", firstLine(sql))
	}
}

func (db *fakeTaskDAGDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	db.mu.Lock()
	defer db.mu.Unlock()

	switch {
	case strings.Contains(sql, "BindRunningTaskDagNodeTurn"):
		values, err := db.bindRunningNodeTurn(args...)
		return stubTaskDAGRow{values: values, err: err}
	case strings.Contains(sql, "CompleteTaskDagNode"):
		values, err := db.completeNode(args...)
		return stubTaskDAGRow{values: values, err: err}
	case strings.Contains(sql, "UpdateTaskDagNodeStatusFlexible"):
		values, err := db.updateNodeStatusFlexible(args...)
		return stubTaskDAGRow{values: values, err: err}
	default:
		return stubTaskDAGRow{err: fmt.Errorf("unexpected QueryRow call: %s", firstLine(sql))}
	}
}

func (db *fakeTaskDAGDB) claimDueWakeups(args ...any) ([][]any, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("claim args len = %d, want 3", len(args))
	}
	claimedBy, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("claimed_by arg = %T", args[0])
	}
	leaseInterval, ok := args[1].(sqlc.Interval)
	if !ok {
		return nil, fmt.Errorf("lease interval arg = %T", args[1])
	}
	limit, ok := args[2].(int32)
	if !ok {
		return nil, fmt.Errorf("limit arg = %T", args[2])
	}

	ids := make([]int64, 0, len(db.wakeups))
	for id, row := range db.wakeups {
		if row.Status != "pending" || row.NextRetryAt.Time.After(db.now) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		left, right := db.wakeups[ids[i]], db.wakeups[ids[j]]
		if !left.NextRetryAt.Time.Equal(right.NextRetryAt.Time) {
			return left.NextRetryAt.Time.Before(right.NextRetryAt.Time)
		}
		return left.ID < right.ID
	})

	rows := make([][]any, 0, min(len(ids), int(limit)))
	leaseExpiresAt := timestamptzValue(db.now.Add(intervalDuration(leaseInterval)))
	for _, id := range ids {
		if len(rows) >= int(limit) {
			break
		}
		row := db.wakeups[id]
		row.Status = "dispatching"
		row.ClaimedAt = timestamptzValue(db.now)
		row.ClaimedBy = claimedBy
		row.LeaseExpiresAt = leaseExpiresAt
		row.AttemptCount++
		row.UpdatedAt = timestamptzValue(db.now)
		db.wakeups[id] = row
		rows = append(rows, taskDagWakeupValues(row))
	}
	return rows, nil
}

func (db *fakeTaskDAGDB) bindWakeupTurn(args ...any) (int64, error) {
	if len(args) != 2 {
		return 0, fmt.Errorf("bind wakeup args len = %d, want 2", len(args))
	}
	turnID, ok := args[0].(sqlc.Text)
	if !ok {
		return 0, fmt.Errorf("bound turn arg = %T", args[0])
	}
	id, ok := args[1].(int64)
	if !ok {
		return 0, fmt.Errorf("wakeup id arg = %T", args[1])
	}
	row, ok := db.wakeups[id]
	if !ok || row.Status != "sent" || !row.SentAt.Valid || row.BoundTurnID.Valid {
		return 0, nil
	}
	row.BoundTurnID = turnID
	row.TurnBoundAt = timestamptzValue(db.now)
	row.UpdatedAt = timestamptzValue(db.now)
	db.wakeups[id] = row
	return 1, nil
}

func (db *fakeTaskDAGDB) markWakeupSent(args ...any) (int64, error) {
	if len(args) != 4 {
		return 0, fmt.Errorf("mark sent args len = %d, want 4", len(args))
	}
	id, ok := args[0].(int64)
	if !ok {
		return 0, fmt.Errorf("wakeup id arg = %T", args[0])
	}
	claimedAt, ok := args[1].(sqlc.Timestamptz)
	if !ok {
		return 0, fmt.Errorf("claimed_at arg = %T", args[1])
	}
	claimedBy, ok := args[2].(string)
	if !ok {
		return 0, fmt.Errorf("claimed_by arg = %T", args[2])
	}
	leaseExpiresAt, ok := args[3].(sqlc.Timestamptz)
	if !ok {
		return 0, fmt.Errorf("lease_expires_at arg = %T", args[3])
	}
	row, ok := db.wakeups[id]
	if !ok || !matchesClaimFence(row, claimedAt, claimedBy, leaseExpiresAt, db.now) {
		return 0, nil
	}
	row.Status = "sent"
	row.SentAt = timestamptzValue(db.now)
	row.UpdatedAt = timestamptzValue(db.now)
	db.wakeups[id] = row
	return 1, nil
}

func (db *fakeTaskDAGDB) retryWakeup(args ...any) (int64, error) {
	if len(args) != 6 {
		return 0, fmt.Errorf("retry args len = %d, want 6", len(args))
	}
	retryInterval, ok := args[0].(sqlc.Interval)
	if !ok {
		return 0, fmt.Errorf("retry interval arg = %T", args[0])
	}
	lastError, ok := args[1].(string)
	if !ok {
		return 0, fmt.Errorf("last_error arg = %T", args[1])
	}
	id, ok := args[2].(int64)
	if !ok {
		return 0, fmt.Errorf("wakeup id arg = %T", args[2])
	}
	claimedAt, ok := args[3].(sqlc.Timestamptz)
	if !ok {
		return 0, fmt.Errorf("claimed_at arg = %T", args[3])
	}
	claimedBy, ok := args[4].(string)
	if !ok {
		return 0, fmt.Errorf("claimed_by arg = %T", args[4])
	}
	leaseExpiresAt, ok := args[5].(sqlc.Timestamptz)
	if !ok {
		return 0, fmt.Errorf("lease_expires_at arg = %T", args[5])
	}
	row, ok := db.wakeups[id]
	if !ok || row.AttemptCount >= 8 || !matchesClaimFence(row, claimedAt, claimedBy, leaseExpiresAt, db.now) {
		return 0, nil
	}
	row.Status = "pending"
	row.NextRetryAt = timestamptzValue(db.now.Add(intervalDuration(retryInterval)))
	row.LastError = lastError
	row.ClaimedAt = sqlc.Timestamptz{}
	row.ClaimedBy = ""
	row.LeaseExpiresAt = sqlc.Timestamptz{}
	row.UpdatedAt = timestamptzValue(db.now)
	db.wakeups[id] = row
	return 1, nil
}

func (db *fakeTaskDAGDB) failWakeup(args ...any) (int64, error) {
	if len(args) != 5 {
		return 0, fmt.Errorf("fail args len = %d, want 5", len(args))
	}
	lastError, ok := args[0].(string)
	if !ok {
		return 0, fmt.Errorf("last_error arg = %T", args[0])
	}
	id, ok := args[1].(int64)
	if !ok {
		return 0, fmt.Errorf("wakeup id arg = %T", args[1])
	}
	claimedAt, ok := args[2].(sqlc.Timestamptz)
	if !ok {
		return 0, fmt.Errorf("claimed_at arg = %T", args[2])
	}
	claimedBy, ok := args[3].(string)
	if !ok {
		return 0, fmt.Errorf("claimed_by arg = %T", args[3])
	}
	leaseExpiresAt, ok := args[4].(sqlc.Timestamptz)
	if !ok {
		return 0, fmt.Errorf("lease_expires_at arg = %T", args[4])
	}
	row, ok := db.wakeups[id]
	if !ok || !matchesClaimFence(row, claimedAt, claimedBy, leaseExpiresAt, db.now) {
		return 0, nil
	}
	row.Status = "failed"
	row.LastError = lastError
	row.ClaimedAt = sqlc.Timestamptz{}
	row.ClaimedBy = ""
	row.LeaseExpiresAt = sqlc.Timestamptz{}
	row.UpdatedAt = timestamptzValue(db.now)
	db.wakeups[id] = row
	return 1, nil
}

func (db *fakeTaskDAGDB) reclaimStaleWakeups() (int64, error) {
	var count int64
	for id, row := range db.wakeups {
		if row.Status != "dispatching" || !row.LeaseExpiresAt.Valid || !row.LeaseExpiresAt.Time.Before(db.now) {
			continue
		}
		row.Status = "pending"
		row.ClaimedAt = sqlc.Timestamptz{}
		row.ClaimedBy = ""
		row.LeaseExpiresAt = sqlc.Timestamptz{}
		row.UpdatedAt = timestamptzValue(db.now)
		db.wakeups[id] = row
		count++
	}
	return count, nil
}

func (db *fakeTaskDAGDB) bindRunningNodeTurn(args ...any) ([]any, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("bind node args len = %d, want 4", len(args))
	}
	turnID, ok := args[0].(sqlc.Text)
	if !ok {
		return nil, fmt.Errorf("turn id arg = %T", args[0])
	}
	dagKey, ok := args[1].(string)
	if !ok {
		return nil, fmt.Errorf("dag key arg = %T", args[1])
	}
	nodeKey, ok := args[2].(string)
	if !ok {
		return nil, fmt.Errorf("node key arg = %T", args[2])
	}
	wakeupID, ok := args[3].(sqlc.Int8)
	if !ok {
		return nil, fmt.Errorf("wakeup id arg = %T", args[3])
	}
	key := dagNodeKey(dagKey, nodeKey)
	row, ok := db.nodes[key]
	if !ok || row.Status != "running" || row.ActiveTurnID.Valid || !sameInt8(row.ActiveWakeupID, wakeupID) {
		return nil, pgx.ErrNoRows
	}
	row.ActiveTurnID = turnID
	row.LastEventAt = timestamptzValue(db.now)
	row.UpdatedAt = timestamptzValue(db.now)
	db.nodes[key] = row
	return taskDagNodeValues(row), nil
}

func (db *fakeTaskDAGDB) completeNode(args ...any) ([]any, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("complete args len = %d, want 4", len(args))
	}
	status, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("status arg = %T", args[0])
	}
	result, ok := args[1].([]byte)
	if !ok {
		return nil, fmt.Errorf("result arg = %T", args[1])
	}
	dagKey, ok := args[2].(string)
	if !ok {
		return nil, fmt.Errorf("dag key arg = %T", args[2])
	}
	nodeKey, ok := args[3].(string)
	if !ok {
		return nil, fmt.Errorf("node key arg = %T", args[3])
	}
	key := dagNodeKey(dagKey, nodeKey)
	row, ok := db.nodes[key]
	if !ok || (row.Status != "running" && row.Status != "awaiting_verify") {
		return nil, pgx.ErrNoRows
	}
	row.Status = status
	row.Result = append([]byte(nil), result...)
	row.ActiveTurnID = sqlc.Text{}
	row.ActiveWakeupID = sqlc.Int8{}
	if !row.FinishedAt.Valid {
		row.FinishedAt = timestamptzValue(db.now)
	}
	row.UpdatedAt = timestamptzValue(db.now)
	db.nodes[key] = row
	return taskDagNodeValues(row), nil
}

func (db *fakeTaskDAGDB) enqueueWakeup(args ...any) (int64, error) {
	if len(args) != 6 {
		return 0, fmt.Errorf("enqueue args len = %d, want 6", len(args))
	}
	dagKey, ok := args[0].(string)
	if !ok {
		return 0, fmt.Errorf("dag key arg = %T", args[0])
	}
	nodeKey, ok := args[1].(string)
	if !ok {
		return 0, fmt.Errorf("node key arg = %T", args[1])
	}
	wakeupKind, ok := args[2].(string)
	if !ok {
		return 0, fmt.Errorf("wakeup_kind arg = %T", args[2])
	}
	targetAgentID, ok := args[3].(string)
	if !ok {
		return 0, fmt.Errorf("target_agent_id arg = %T", args[3])
	}
	payload, ok := args[4].([]byte)
	if !ok {
		return 0, fmt.Errorf("payload arg = %T", args[4])
	}
	idempotencyKey, ok := args[5].(string)
	if !ok {
		return 0, fmt.Errorf("idempotency_key arg = %T", args[5])
	}
	// Simulate `ON CONFLICT (idempotency_key) DO NOTHING` by scanning existing rows.
	for _, existing := range db.wakeups {
		if existing.IdempotencyKey == idempotencyKey {
			return 0, nil
		}
	}
	db.wakeupSeq++
	id := db.wakeupSeq
	db.wakeups[id] = sqlc.TaskDagWakeup{
		ID:             id,
		DagKey:         dagKey,
		NodeKey:        nodeKey,
		WakeupKind:     wakeupKind,
		TargetAgentID:  targetAgentID,
		PromptPayload:  append([]byte(nil), payload...),
		IdempotencyKey: idempotencyKey,
		Status:         "pending",
		NextRetryAt:    timestamptzValue(db.now),
		CreatedAt:      timestamptzValue(db.now),
		UpdatedAt:      timestamptzValue(db.now),
	}
	return 1, nil
}

func (db *fakeTaskDAGDB) listTaskDagNodes(args ...any) ([][]any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("list nodes args len = %d, want 1", len(args))
	}
	dagKey, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("dag key arg = %T", args[0])
	}
	keys := make([]string, 0, len(db.nodes))
	for k, row := range db.nodes {
		if row.DagKey != dagKey {
			continue
		}
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := db.nodes[keys[i]], db.nodes[keys[j]]
		if !left.CreatedAt.Time.Equal(right.CreatedAt.Time) {
			return left.CreatedAt.Time.Before(right.CreatedAt.Time)
		}
		return left.ID < right.ID
	})
	rows := make([][]any, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, taskDagNodeValues(db.nodes[k]))
	}
	return rows, nil
}

func (db *fakeTaskDAGDB) updateNodeStatusFlexible(args ...any) ([]any, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("flexible update args len = %d, want 4", len(args))
	}
	status, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("status arg = %T", args[0])
	}
	result, ok := args[1].([]byte)
	if !ok {
		return nil, fmt.Errorf("result arg = %T", args[1])
	}
	dagKey, ok := args[2].(string)
	if !ok {
		return nil, fmt.Errorf("dag key arg = %T", args[2])
	}
	nodeKey, ok := args[3].(string)
	if !ok {
		return nil, fmt.Errorf("node key arg = %T", args[3])
	}
	key := dagNodeKey(dagKey, nodeKey)
	row, ok := db.nodes[key]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	row.Status = status
	row.Result = append([]byte(nil), result...)
	row.UpdatedAt = timestamptzValue(db.now)
	db.nodes[key] = row
	return taskDagNodeValues(row), nil
}

func matchesClaimFence(row sqlc.TaskDagWakeup, claimedAt sqlc.Timestamptz, claimedBy string, leaseExpiresAt sqlc.Timestamptz, now time.Time) bool {
	return row.Status == "dispatching" &&
		sameTimestamp(row.ClaimedAt, claimedAt) &&
		row.ClaimedBy == claimedBy &&
		sameTimestamp(row.LeaseExpiresAt, leaseExpiresAt) &&
		row.LeaseExpiresAt.Valid &&
		!row.LeaseExpiresAt.Time.Before(now)
}

func updateTag(count int64, err error) (pgconn.CommandTag, error) {
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", count)), nil
}

func dagNodeKey(dagKey, nodeKey string) string { return dagKey + "\x00" + nodeKey }

func firstLine(sql string) string {
	if idx := strings.IndexByte(sql, '\n'); idx >= 0 {
		return sql[:idx]
	}
	return sql
}
