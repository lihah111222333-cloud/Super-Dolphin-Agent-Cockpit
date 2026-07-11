//go:build legacy_pg_fake

package taskdag

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

func (db *fakeTaskDAGDB) claimableWakeupIDs() []int64 {
	ids := make([]int64, 0, len(db.wakeups))
	for id, row := range db.wakeups {
		if row.Status != "pending" || row.NextRetryAt.Time.After(db.now) {
			continue
		}
		if db.fakeWakeupHasClaimableRun(row) {
			ids = append(ids, id)
		}
	}
	return ids
}

func (db *fakeTaskDAGDB) sortWakeupIDsByRetry(ids []int64) {
	sort.Slice(ids, func(i, j int) bool {
		left, right := db.wakeups[ids[i]], db.wakeups[ids[j]]
		if !left.NextRetryAt.Time.Equal(right.NextRetryAt.Time) {
			return left.NextRetryAt.Time.Before(right.NextRetryAt.Time)
		}
		return left.ID < right.ID
	})
}

func (db *fakeTaskDAGDB) claimWakeupRows(ids []int64, limit int, claimedBy string, leaseInterval sqlc.Interval) [][]any {
	rows := make([][]any, 0, min(len(ids), limit))
	leaseExpiresAt := timestamptzValue(db.now.Add(intervalDuration(leaseInterval)))
	for _, id := range ids {
		if len(rows) >= limit {
			break
		}
		row := db.claimWakeup(id, claimedBy, leaseExpiresAt)
		rows = append(rows, taskDagWakeupValues(row))
	}
	return rows
}

func (db *fakeTaskDAGDB) claimWakeup(id int64, claimedBy string, leaseExpiresAt sqlc.Timestamptz) sqlc.TaskDagWakeup {
	row := db.wakeups[id]
	row.Status = "dispatching"
	row.ClaimedAt = timestamptzValue(db.now)
	row.ClaimedBy = claimedBy
	row.LeaseExpiresAt = leaseExpiresAt
	row.AttemptCount++
	row.UpdatedAt = timestamptzValue(db.now)
	db.wakeups[id] = row
	return row
}

func isFakeCompletableStatus(status string) bool {
	switch status {
	case "ready", "running":
		return true
	default:
		return false
	}
}

func isFakeReadyToRunStatus(status string) bool {
	return status == "pending" || status == "ready"
}

func (db *fakeTaskDAGDB) hasWakeupIdempotencyKey(idempotencyKey string) bool {
	for _, existing := range db.wakeups {
		if existing.IdempotencyKey == idempotencyKey {
			return true
		}
	}
	return false
}

func decodeRunEventPayload(payload []byte) (any, error) {
	var newEvent any
	if err := json.Unmarshal(payload, &newEvent); err != nil {
		return nil, fmt.Errorf("decode event payload: %w", err)
	}
	return newEvent, nil
}

func appendRunEventPayload(run sqlc.TaskDagRun, newEvent any) (sqlc.TaskDagRun, error) {
	arr := decodeExistingRunEvents(run.Events)
	arr = append(arr, newEvent)
	encoded, err := json.Marshal(trimRunEvents(arr))
	if err != nil {
		return run, fmt.Errorf("encode events array: %w", err)
	}
	run.Events = encoded
	return run, nil
}

func decodeExistingRunEvents(raw []byte) []any {
	var arr []any
	if len(raw) == 0 {
		return arr
	}
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil
	}
	return arr
}

func trimRunEvents(arr []any) []any {
	const ringCap = 50
	if len(arr) > ringCap {
		return arr[len(arr)-ringCap:]
	}
	return arr
}

func (db *fakeTaskDAGDB) claimDueWakeups(args ...any) ([][]any, error) {
	if err := requireFakeTaskDAGArgs(args, 3, "claim",
		fakeTaskDAGTypedArg[string](0, "claimed_by"),
		fakeTaskDAGTypedArg[sqlc.Interval](1, "lease interval"),
		fakeTaskDAGTypedArg[int32](2, "limit")); err != nil {
		return nil, err
	}
	claimedBy := args[0].(string)
	leaseInterval := args[1].(sqlc.Interval)
	limit := args[2].(int32)
	ids := db.claimableWakeupIDs()
	db.sortWakeupIDsByRetry(ids)
	return db.claimWakeupRows(ids, int(limit), claimedBy, leaseInterval), nil
}

func (db *fakeTaskDAGDB) fakeWakeupHasClaimableRun(row sqlc.TaskDagWakeup) bool {
	if strings.TrimSpace(row.DagKey) == "" || strings.TrimSpace(row.NodeKey) == "" {
		return true
	}
	if !row.RunID.Valid || row.RunID.Int64 <= 0 {
		return false
	}
	for _, run := range db.runs {
		if run.ID == row.RunID.Int64 && run.DagKey == row.DagKey && run.Status == "running" {
			return true
		}
	}
	return false
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
	if err := requireFakeTaskDAGArgs(args, 6, "retry",
		fakeTaskDAGTypedArg[sqlc.Interval](0, "retry interval"),
		fakeTaskDAGTypedArg[string](1, "last_error"),
		fakeTaskDAGTypedArg[int64](2, "wakeup id"),
		fakeTaskDAGTypedArg[sqlc.Timestamptz](3, "claimed_at"),
		fakeTaskDAGTypedArg[string](4, "claimed_by"),
		fakeTaskDAGTypedArg[sqlc.Timestamptz](5, "lease_expires_at")); err != nil {
		return 0, err
	}
	retryInterval := args[0].(sqlc.Interval)
	lastError := args[1].(string)
	id := args[2].(int64)
	claimedAt := args[3].(sqlc.Timestamptz)
	claimedBy := args[4].(string)
	leaseExpiresAt := args[5].(sqlc.Timestamptz)
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
