//go:build legacy_pg_fake

package taskdag

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/jackc/pgx/v5"
)

func (db *fakeTaskDAGDB) bindRunningNodeTurn(args ...any) ([]any, error) {
	if err := requireFakeTaskDAGArgs(args, 5, "bind node",
		fakeTaskDAGTypedArg[sqlc.Text](0, "turn id"),
		fakeTaskDAGTypedArg[string](1, "dag key"),
		fakeTaskDAGTypedArg[string](2, "node key"),
		fakeTaskDAGTypedArg[sqlc.Int8](3, "wakeup id"),
		fakeTaskDAGInt8Arg(4, "run id")); err != nil {
		return nil, err
	}
	turnID := args[0].(sqlc.Text)
	dagKey := args[1].(string)
	nodeKey := args[2].(string)
	wakeupID := args[3].(sqlc.Int8)
	runID, err := fakeInt8Arg(args, 4, "run id")
	if err != nil {
		return nil, err
	}
	key := dagNodeLookupKey(dagKey, nodeKey, runID)
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
	if err := requireFakeTaskDAGArgs(args, 5, "complete",
		fakeTaskDAGTypedArg[string](0, "status"),
		fakeTaskDAGTypedArg[[]byte](1, "result"),
		fakeTaskDAGTypedArg[string](2, "dag key"),
		fakeTaskDAGTypedArg[string](3, "node key"),
		fakeTaskDAGInt8Arg(4, "run id")); err != nil {
		return nil, err
	}
	status := args[0].(string)
	result := args[1].([]byte)
	dagKey := args[2].(string)
	nodeKey := args[3].(string)
	runID, err := fakeInt8Arg(args, 4, "run id")
	if err != nil {
		return nil, err
	}
	key := dagNodeLookupKey(dagKey, nodeKey, runID)
	row, ok := db.nodes[key]
	// ADR-017 v1.2 搂2.3 鐧藉悕鍗曟墿 'ready'銆?
	if !ok || !isFakeCompletableStatus(row.Status) {
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
	if err := requireFakeTaskDAGArgs(args, 7, "enqueue",
		fakeTaskDAGTypedArg[string](0, "dag key"),
		fakeTaskDAGTypedArg[string](1, "node key"),
		fakeTaskDAGInt8Arg(2, "run_id"),
		fakeTaskDAGTypedArg[string](3, "wakeup_kind"),
		fakeTaskDAGTypedArg[string](4, "target_agent_id"),
		fakeTaskDAGTypedArg[[]byte](5, "payload"),
		fakeTaskDAGTypedArg[string](6, "idempotency_key")); err != nil {
		return 0, err
	}
	dagKey := args[0].(string)
	nodeKey := args[1].(string)
	runID, err := fakeInt8Arg(args, 2, "run_id")
	if err != nil {
		return 0, err
	}
	wakeupKind := args[3].(string)
	targetAgentID := args[4].(string)
	payload := args[5].([]byte)
	idempotencyKey := args[6].(string)
	if db.hasWakeupIdempotencyKey(idempotencyKey) {
		return 0, nil
	}
	db.wakeupSeq++
	id := db.wakeupSeq
	db.wakeups[id] = sqlc.TaskDagWakeup{
		ID:             id,
		DagKey:         dagKey,
		NodeKey:        nodeKey,
		RunID:          sqlc.Int8ValuePtr(nilIfZero(runID)),
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

func (db *fakeTaskDAGDB) lookupNodesBySpawningThread(args ...any) ([][]any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("lookup-by-spawning args len = %d, want 1", len(args))
	}
	threadID, err := fakeTextArg(args, 0, "thread id")
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0)
	for k, row := range db.nodes {
		if !row.SpawningThreadID.Valid {
			continue
		}
		if !row.RunID.Valid || row.RunID.Int64 <= 0 {
			continue
		}
		if row.SpawningThreadID.String != threadID {
			continue
		}
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := db.nodes[keys[i]], db.nodes[keys[j]]
		if !left.UpdatedAt.Time.Equal(right.UpdatedAt.Time) {
			return left.UpdatedAt.Time.After(right.UpdatedAt.Time)
		}
		return left.ID > right.ID
	})
	rows := make([][]any, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, taskDagNodeValues(db.nodes[k]))
	}
	return rows, nil
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
		if row.DagKey != dagKey || row.RunID.Valid {
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

func (db *fakeTaskDAGDB) listTaskDagRunNodes(args ...any) ([][]any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("list run nodes args len = %d, want 2", len(args))
	}
	dagKey, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("dag key arg = %T", args[0])
	}
	runID, err := fakeInt8Arg(args, 1, "run id")
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(db.nodes))
	for k, row := range db.nodes {
		if row.DagKey != dagKey || fakeRunID(row) != runID {
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

func (db *fakeTaskDAGDB) updateRunningNodeStatus(args ...any) ([]any, error) {
	if err := requireFakeTaskDAGArgs(args, 6, "running update",
		fakeTaskDAGTypedArg[string](0, "status"),
		fakeTaskDAGTypedArg[[]byte](1, "result"),
		fakeTaskDAGTypedArg[sqlc.Int8](2, "wakeup id"),
		fakeTaskDAGTypedArg[string](3, "dag key"),
		fakeTaskDAGTypedArg[string](4, "node key"),
		fakeTaskDAGInt8Arg(5, "run id")); err != nil {
		return nil, err
	}
	status := args[0].(string)
	result := args[1].([]byte)
	wakeupID := args[2].(sqlc.Int8)
	dagKey := args[3].(string)
	nodeKey := args[4].(string)
	runID, err := fakeInt8Arg(args, 5, "run id")
	if err != nil {
		return nil, err
	}
	key := dagNodeLookupKey(dagKey, nodeKey, runID)
	row, ok := db.nodes[key]
	if !ok || !isFakeReadyToRunStatus(row.Status) {
		return nil, pgx.ErrNoRows
	}
	row.Status = status
	row.Result = append([]byte(nil), result...)
	row.ActiveTurnID = sqlc.Text{}
	row.ActiveWakeupID = wakeupID
	row.LastEventAt = sqlc.Timestamptz{}
	if !row.StartedAt.Valid {
		row.StartedAt = timestamptzValue(db.now)
	}
	row.UpdatedAt = timestamptzValue(db.now)
	db.nodes[key] = row
	return taskDagNodeValues(row), nil
}

func (db *fakeTaskDAGDB) updateNodeStatusFlexible(args ...any) ([]any, error) {
	if len(args) != 5 {
		return nil, fmt.Errorf("flexible update args len = %d, want 5", len(args))
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
	runID, err := fakeInt8Arg(args, 4, "run id")
	if err != nil {
		return nil, err
	}
	key := dagNodeLookupKey(dagKey, nodeKey, runID)
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

func (db *fakeTaskDAGDB) patchNodeConfigIfUnchanged(args ...any) ([]any, error) {
	if len(args) != 5 {
		return nil, fmt.Errorf("patch config args len = %d, want 5", len(args))
	}
	config, ok := args[0].([]byte)
	if !ok {
		return nil, fmt.Errorf("config arg = %T", args[0])
	}
	dagKey, ok := args[1].(string)
	if !ok {
		return nil, fmt.Errorf("dag key arg = %T", args[1])
	}
	nodeKey, ok := args[2].(string)
	if !ok {
		return nil, fmt.Errorf("node key arg = %T", args[2])
	}
	runID, err := fakeInt8Arg(args, 3, "run id")
	if err != nil {
		return nil, err
	}
	previousConfig, ok := args[4].([]byte)
	if !ok {
		return nil, fmt.Errorf("previous config arg = %T", args[4])
	}
	key := dagNodeLookupKey(dagKey, nodeKey, runID)
	row, ok := db.nodes[key]
	if !ok || isFakeTerminalStatus(row.Status) || !jsonBytesEqual(row.Config, previousConfig) {
		return nil, pgx.ErrNoRows
	}
	row.Config = append([]byte(nil), config...)
	row.UpdatedAt = timestamptzValue(db.now)
	db.nodes[key] = row
	return taskDagNodeValues(row), nil
}

func jsonBytesEqual(left, right []byte) bool {
	var leftValue any
	var rightValue any
	if err := json.Unmarshal(left, &leftValue); err != nil {
		return string(left) == string(right)
	}
	if err := json.Unmarshal(right, &rightValue); err != nil {
		return string(left) == string(right)
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func (db *fakeTaskDAGDB) claimNodeOutputMaterialization(args ...any) ([]any, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("claim output materialization args len = %d, want 4", len(args))
	}
	result, ok := args[0].([]byte)
	if !ok {
		return nil, fmt.Errorf("result arg = %T", args[0])
	}
	dagKey, ok := args[1].(string)
	if !ok {
		return nil, fmt.Errorf("dag key arg = %T", args[1])
	}
	nodeKey, ok := args[2].(string)
	if !ok {
		return nil, fmt.Errorf("node key arg = %T", args[2])
	}
	runID, err := fakeInt8Arg(args, 3, "run id")
	if err != nil {
		return nil, err
	}
	key := dagNodeLookupKey(dagKey, nodeKey, runID)
	row, ok := db.nodes[key]
	if !ok || (row.Status != "ready" && row.Status != "running" && row.Status != "awaiting_verify") {
		return nil, pgx.ErrNoRows
	}
	row.Status = "awaiting_verify"
	row.Result = append([]byte(nil), result...)
	row.UpdatedAt = timestamptzValue(db.now)
	db.nodes[key] = row
	return taskDagNodeValues(row), nil
}

func (db *fakeTaskDAGDB) failNodeIfNonTerminal(args ...any) ([]any, error) {
	if len(args) != 5 {
		return nil, fmt.Errorf("fail non-terminal args len = %d, want 5", len(args))
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
	runID, err := fakeInt8Arg(args, 4, "run id")
	if err != nil {
		return nil, err
	}
	key := dagNodeLookupKey(dagKey, nodeKey, runID)
	if db.shouldRunBeforeFailHook(key) {
		db.beforeFailNonTerminal(dagKey, nodeKey)
	}
	row, ok := db.nodes[key]
	if !ok || isFakeTerminalStatus(row.Status) {
		return nil, pgx.ErrNoRows
	}
	row.Status = status
	row.Result = append([]byte(nil), result...)
	row.UpdatedAt = timestamptzValue(db.now)
	db.nodes[key] = row
	return taskDagNodeValues(row), nil
}

func (db *fakeTaskDAGDB) shouldRunBeforeFailHook(key string) bool {
	return db.beforeFailNonTerminal != nil && !db.locks[key]
}

func isFakeTerminalStatus(status string) bool {
	switch status {
	case "done", "failed", "cancelled", "skipped":
		return true
	default:
		return false
	}
}

func matchesClaimFence(row sqlc.TaskDagWakeup, claimedAt sqlc.Timestamptz, claimedBy string, leaseExpiresAt sqlc.Timestamptz, now time.Time) bool {
	return row.Status == "dispatching" &&
		sameTimestamp(row.ClaimedAt, claimedAt) &&
		row.ClaimedBy == claimedBy &&
		sameTimestamp(row.LeaseExpiresAt, leaseExpiresAt) &&
		row.LeaseExpiresAt.Valid &&
		!row.LeaseExpiresAt.Time.Before(now)
}
