package taskdag

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeTaskDAGDB struct {
	mu      sync.Mutex
	now     time.Time
	dags    map[string]sqlc.TaskDag
	wakeups map[int64]sqlc.TaskDagWakeup
	nodes   map[string]sqlc.TaskDagNode
	// F6.2: runs 用于模拟 task_dag_runs 一行，键是 run_key；finalize SQL 拦截会读写它。
	// F6.2: runs simulates task_dag_runs rows keyed by run_key so the finalize
	// SQL interceptor can mutate run.status when all nodes reach terminal.
	runs                  map[string]sqlc.TaskDagRun
	beforeFailNonTerminal func(dagKey, nodeKey string)
	wakeupSeq             int64
	runSeq                int64
}

func newFakeTaskDAGDB(now time.Time) *fakeTaskDAGDB {
	return &fakeTaskDAGDB{
		now:     now.UTC(),
		dags:    make(map[string]sqlc.TaskDag),
		wakeups: make(map[int64]sqlc.TaskDagWakeup),
		nodes:   make(map[string]sqlc.TaskDagNode),
		runs:    make(map[string]sqlc.TaskDagRun),
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
	case strings.Contains(sql, "CascadeFailPendingTaskDagNode"):
		return updateTag(db.cascadeFailPendingNode(args...))
	case strings.Contains(sql, "PromoteSingleNodePendingToReady"):
		return updateTag(db.promoteSingleNodePendingToReady(args...))
	case strings.Contains(sql, "DeleteTaskDagNode"):
		return updateTag(db.deleteTaskDagNode(args...))
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec call: %s", firstLine(sql))
	}
}

func (db *fakeTaskDAGDB) deleteTaskDagNode(args ...any) (int64, error) {
	if len(args) != 2 {
		return 0, fmt.Errorf("delete node args len = %d, want 2", len(args))
	}
	dagKey, ok := args[0].(string)
	if !ok {
		return 0, fmt.Errorf("dag key arg = %T", args[0])
	}
	nodeKey, ok := args[1].(string)
	if !ok {
		return 0, fmt.Errorf("node key arg = %T", args[1])
	}
	key := dagNodeKey(dagKey, nodeKey)
	row, ok := db.nodes[key]
	if !ok {
		return 0, nil
	}
	if row.Status != "pending" && row.Status != "ready" {
		return 0, nil
	}
	delete(db.nodes, key)
	return 1, nil
}

func (db *fakeTaskDAGDB) countActiveTaskDagRunsByKey(args ...any) ([]any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("count active runs args len = %d, want 1", len(args))
	}
	dagKey, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("dag key arg = %T", args[0])
	}
	var active int64
	for _, run := range db.runs {
		if run.DagKey == dagKey && run.Status == "running" {
			active++
		}
	}
	return []any{active}, nil
}

// promoteSingleNodePendingToReady 复现 F6.3 PromoteSingleNodePendingToReady SQL
// 的语义：仅当 node 还在 pending 时才推进到 ready，并返回受影响行数。
// promoteSingleNodePendingToReady mirrors the F6.3 SQL: only flip when the
// node row is still in 'pending', otherwise return 0 affected rows.
func (db *fakeTaskDAGDB) promoteSingleNodePendingToReady(args ...any) (int64, error) {
	if len(args) != 2 {
		return 0, fmt.Errorf("promote args len = %d, want 2", len(args))
	}
	dagKey, ok := args[0].(string)
	if !ok {
		return 0, fmt.Errorf("dag key arg = %T", args[0])
	}
	nodeKey, ok := args[1].(string)
	if !ok {
		return 0, fmt.Errorf("node key arg = %T", args[1])
	}
	key := dagNodeKey(dagKey, nodeKey)
	row, ok := db.nodes[key]
	if !ok || row.Status != "pending" {
		return 0, nil
	}
	row.Status = "ready"
	row.UpdatedAt = timestamptzValue(db.now)
	db.nodes[key] = row
	return 1, nil
}

func (db *fakeTaskDAGDB) cascadeFailPendingNode(args ...any) (int64, error) {
	if len(args) != 3 {
		return 0, fmt.Errorf("cascade fail args len = %d, want 3", len(args))
	}
	result, ok := args[0].([]byte)
	if !ok {
		return 0, fmt.Errorf("result arg = %T", args[0])
	}
	dagKey, ok := args[1].(string)
	if !ok {
		return 0, fmt.Errorf("dag key arg = %T", args[1])
	}
	nodeKey, ok := args[2].(string)
	if !ok {
		return 0, fmt.Errorf("node key arg = %T", args[2])
	}
	key := dagNodeKey(dagKey, nodeKey)
	if db.beforeFailNonTerminal != nil {
		db.beforeFailNonTerminal(dagKey, nodeKey)
	}
	row, ok := db.nodes[key]
	if !ok || row.Status != "pending" {
		return 0, nil
	}
	row.Status = "failed"
	row.Result = append([]byte(nil), result...)
	row.UpdatedAt = timestamptzValue(db.now)
	db.nodes[key] = row
	return 1, nil
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
	case strings.Contains(sql, "LookupNodesBySpawningThread"):
		rows, err := db.lookupNodesBySpawningThread(args...)
		if err != nil {
			return nil, err
		}
		return &stubTaskDAGRows{rows: rows}, nil
	case strings.Contains(sql, "FinalizeTaskDagRunIfAllNodesTerminal"):
		rows, err := db.finalizeRunIfAllNodesTerminal(args...)
		if err != nil {
			return nil, err
		}
		return &stubTaskDAGRows{rows: rows}, nil
	default:
		return nil, fmt.Errorf("unexpected Query call: %s", firstLine(sql))
	}
}

// finalizeRunIfAllNodesTerminal 复现 F6.2 SQL 的语义：在同一个 fake DB 上扫
// dag_key 下节点状态，按优先级 (failed > cancelled > succeeded) 决定
// final_status；节点还有非终态或 dag_key 下无 running run 时返回 0 行。
//
// finalizeRunIfAllNodesTerminal mirrors the F6.2 finalize SQL semantics inside
// the fake DB. Empty result rows mean either some nodes are still non-terminal
// or no 'running' run exists for dag_key.
func (db *fakeTaskDAGDB) finalizeRunIfAllNodesTerminal(args ...any) ([][]any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("finalize args len = %d, want 1", len(args))
	}
	dagKey, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("finalize dag_key arg = %T", args[0])
	}
	var total, nonTerminal, failedCnt, cancelledCnt int
	for _, n := range db.nodes {
		if n.DagKey != dagKey {
			continue
		}
		total++
		switch n.Status {
		case "done", "skipped":
			// terminal success
		case "failed":
			failedCnt++
		case "cancelled":
			cancelledCnt++
		default:
			nonTerminal++
		}
	}
	if total == 0 || nonTerminal > 0 {
		return nil, nil
	}
	var finalStatus string
	switch {
	case failedCnt > 0:
		finalStatus = "failed"
	case cancelledCnt > 0:
		finalStatus = "cancelled"
	default:
		finalStatus = "succeeded"
	}
	rows := make([][]any, 0, 1)
	for runKey, run := range db.runs {
		if run.DagKey != dagKey || run.Status != "running" {
			continue
		}
		run.Status = finalStatus
		run.FinishedAt = timestamptzValue(db.now)
		run.UpdatedAt = timestamptzValue(db.now)
		if finalStatus == "succeeded" {
			metadata, err := db.metadataWithFinalOutput(dagKey, run.Metadata)
			if err != nil {
				return nil, err
			}
			run.Metadata = metadata
		}
		db.runs[runKey] = run
		rows = append(rows, []any{run.RunKey, run.Status})
	}
	return rows, nil
}

func (db *fakeTaskDAGDB) metadataWithFinalOutput(dagKey string, metadata []byte) ([]byte, error) {
	dag, ok := db.dags[dagKey]
	if !ok || len(dag.Metadata) == 0 {
		return metadata, nil
	}
	var dagMeta struct {
		FinalNodeKey string `json:"final_node_key"`
	}
	if err := json.Unmarshal(dag.Metadata, &dagMeta); err != nil {
		return nil, fmt.Errorf("decode dag metadata: %w", err)
	}
	if dagMeta.FinalNodeKey == "" {
		return metadata, nil
	}
	node, ok := db.nodes[dagNodeKey(dagKey, dagMeta.FinalNodeKey)]
	if !ok {
		return metadata, nil
	}
	finalOutput, ok, err := finalOutputFromNodeResult(node)
	if err != nil {
		return nil, err
	}
	if !ok {
		return metadata, nil
	}
	runMetadata := map[string]any{}
	if len(metadata) > 0 {
		var raw any
		if err := json.Unmarshal(metadata, &raw); err != nil {
			return nil, fmt.Errorf("decode run metadata: %w", err)
		}
		if obj, ok := raw.(map[string]any); ok {
			runMetadata = obj
		}
	}
	runMetadata["final_output"] = finalOutput
	encoded, err := json.Marshal(runMetadata)
	if err != nil {
		return nil, fmt.Errorf("encode run metadata: %w", err)
	}
	return encoded, nil
}

func finalOutputFromNodeResult(node sqlc.TaskDagNode) (map[string]any, bool, error) {
	title := node.Title
	if title == "" {
		title = "Final output"
	}
	configuredPath := configuredSharedfilePathFromNodeConfig(node.Config)
	out := map[string]any{
		"role":            "final_output",
		"title":           title,
		"source_node_key": node.NodeKey,
	}
	if len(node.Result) == 0 {
		if configuredPath == "" {
			return nil, false, nil
		}
		out["kind"] = "file"
		out["path"] = configuredPath
		return out, true, nil
	}
	var result any
	if err := json.Unmarshal(node.Result, &result); err != nil {
		return nil, false, fmt.Errorf("decode final node result: %w", err)
	}
	switch typed := result.(type) {
	case map[string]any:
		if sf, ok := typed["sharedfile"].(map[string]any); ok {
			if path, ok := sf["path"].(string); ok && path != "" {
				out["kind"] = "file"
				out["path"] = path
				return out, true, nil
			}
		}
		if configuredPath != "" {
			out["kind"] = "file"
			out["path"] = configuredPath
			return out, true, nil
		}
		out["kind"] = "json"
		out["result"] = result
	case string:
		if configuredPath != "" {
			out["kind"] = "file"
			out["path"] = configuredPath
			return out, true, nil
		}
		out["kind"] = "text"
		out["text"] = typed
	default:
		if configuredPath != "" {
			out["kind"] = "file"
			out["path"] = configuredPath
			return out, true, nil
		}
		out["kind"] = "json"
		out["result"] = result
	}
	return out, true, nil
}

func configuredSharedfilePathFromNodeConfig(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var cfg struct {
		Outputs struct {
			ToSharedfile *struct {
				Path string `json:"path"`
			} `json:"to_sharedfile"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg.Outputs.ToSharedfile == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Outputs.ToSharedfile.Path)
}

func (db *fakeTaskDAGDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	db.mu.Lock()
	defer db.mu.Unlock()

	switch {
	case strings.Contains(sql, "CountActiveTaskDagRunsByKey"):
		values, err := db.countActiveTaskDagRunsByKey(args...)
		return stubTaskDAGRow{values: values, err: err}
	case strings.Contains(sql, "BindRunningTaskDagNodeTurn"):
		values, err := db.bindRunningNodeTurn(args...)
		return stubTaskDAGRow{values: values, err: err}
	case strings.Contains(sql, "CompleteTaskDagNode"):
		values, err := db.completeNode(args...)
		return stubTaskDAGRow{values: values, err: err}
	case strings.Contains(sql, "FailTaskDagNodeIfNonTerminal"):
		values, err := db.failNodeIfNonTerminal(args...)
		return stubTaskDAGRow{values: values, err: err}
	case strings.Contains(sql, "PatchTaskDagNodeConfigIfUnchanged"):
		values, err := db.patchNodeConfigIfUnchanged(args...)
		return stubTaskDAGRow{values: values, err: err}
	case strings.Contains(sql, "ClaimTaskDagNodeOutputMaterialization"):
		values, err := db.claimNodeOutputMaterialization(args...)
		return stubTaskDAGRow{values: values, err: err}
	case strings.Contains(sql, "UpdateTaskDagNodeStatusFlexible"):
		values, err := db.updateNodeStatusFlexible(args...)
		return stubTaskDAGRow{values: values, err: err}
	case strings.Contains(sql, "UpdateRunningTaskDagNodeStatus"):
		values, err := db.updateRunningNodeStatus(args...)
		return stubTaskDAGRow{values: values, err: err}
	case strings.Contains(sql, "UpdateTaskDagNodeSpawningThread"):
		// F1.5: CTE 同时返回新及旧 spawning_thread_id。
		values, err := db.updateNodeSpawningThread(args...)
		return stubTaskDAGRow{values: values, err: err}
	case strings.Contains(sql, "AppendTaskDagRunEvent") ||
		strings.Contains(sql, "jsonb_build_array($2::jsonb)") ||
		(strings.Contains(sql, "task_dag_runs") && strings.Contains(sql, "jsonb_array_length(events ||")):
		// F1.5: 向 running run.events jsonb 数组 append 一条 event。
		values, err := db.appendRunEvent(args...)
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
	// ADR-017 v1.2 §2.3 白名单扩 'ready'。
	if !ok || (row.Status != "ready" && row.Status != "running" && row.Status != "awaiting_verify") {
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

// lookupNodesBySpawningThread mirrors the ADR-017 §2.2 reverse-lookup query:
// SELECT * FROM task_dag_nodes WHERE spawning_thread_id = $1 AND
// spawning_thread_id IS NOT NULL ORDER BY updated_at DESC, id DESC.
func (db *fakeTaskDAGDB) lookupNodesBySpawningThread(args ...any) ([][]any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("lookup-by-spawning args len = %d, want 1", len(args))
	}
	threadID, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("thread id arg = %T", args[0])
	}
	keys := make([]string, 0)
	for k, row := range db.nodes {
		if !row.SpawningThreadID.Valid {
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

// updateRunningNodeStatus mirrors the W4-fence UpdateRunningTaskDagNodeStatus
// SQL: only flip when current status is in ('pending','ready') (matches the
// production fence post W4 fix). Returns pgx.ErrNoRows otherwise so the store
// surfaces the same "not in fence" path as production.
func (db *fakeTaskDAGDB) updateRunningNodeStatus(args ...any) ([]any, error) {
	if len(args) != 5 {
		return nil, fmt.Errorf("running update args len = %d, want 5", len(args))
	}
	status, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("status arg = %T", args[0])
	}
	result, ok := args[1].([]byte)
	if !ok {
		return nil, fmt.Errorf("result arg = %T", args[1])
	}
	wakeupID, ok := args[2].(sqlc.Int8)
	if !ok {
		return nil, fmt.Errorf("wakeup id arg = %T", args[2])
	}
	dagKey, ok := args[3].(string)
	if !ok {
		return nil, fmt.Errorf("dag key arg = %T", args[3])
	}
	nodeKey, ok := args[4].(string)
	if !ok {
		return nil, fmt.Errorf("node key arg = %T", args[4])
	}
	key := dagNodeKey(dagKey, nodeKey)
	row, ok := db.nodes[key]
	if !ok || (row.Status != "pending" && row.Status != "ready") {
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

func (db *fakeTaskDAGDB) patchNodeConfigIfUnchanged(args ...any) ([]any, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("patch config args len = %d, want 4", len(args))
	}
	dagKey, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("dag key arg = %T", args[0])
	}
	nodeKey, ok := args[1].(string)
	if !ok {
		return nil, fmt.Errorf("node key arg = %T", args[1])
	}
	config, ok := args[2].([]byte)
	if !ok {
		return nil, fmt.Errorf("config arg = %T", args[2])
	}
	previousConfig, ok := args[3].([]byte)
	if !ok {
		return nil, fmt.Errorf("previous config arg = %T", args[3])
	}
	key := dagNodeKey(dagKey, nodeKey)
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
	if len(args) != 3 {
		return nil, fmt.Errorf("claim output materialization args len = %d, want 3", len(args))
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
	key := dagNodeKey(dagKey, nodeKey)
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
	if len(args) != 4 {
		return nil, fmt.Errorf("fail non-terminal args len = %d, want 4", len(args))
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
	if db.beforeFailNonTerminal != nil {
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

func updateTag(count int64, err error) (pgconn.CommandTag, error) {
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", count)), nil
}

func dagNodeKey(dagKey, nodeKey string) string { return dagKey + "\x00" + nodeKey }

// updateNodeSpawningThread mirrors the F1.5 CTE SQL: capture previous value
// then UPDATE. The CTE row has 20 columns (18 node columns +
// spawning_thread_id + previous_spawning_thread_id); the order must match
// task_dag_node_spawning_thread.sql.go's Scan call so stubTaskDAGRow can
// assign cleanly.
func (db *fakeTaskDAGDB) updateNodeSpawningThread(args ...any) ([]any, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("spawning thread args len = %d, want 3", len(args))
	}
	spawningThread, ok := args[0].(sqlc.Text)
	if !ok {
		return nil, fmt.Errorf("spawning_thread_id arg = %T", args[0])
	}
	dagKey, ok := args[1].(string)
	if !ok {
		return nil, fmt.Errorf("dag key arg = %T", args[1])
	}
	nodeKey, ok := args[2].(string)
	if !ok {
		return nil, fmt.Errorf("node key arg = %T", args[2])
	}
	key := dagNodeKey(dagKey, nodeKey)
	row, ok := db.nodes[key]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	prev := row.SpawningThreadID
	row.SpawningThreadID = spawningThread
	row.UpdatedAt = timestamptzValue(db.now)
	db.nodes[key] = row
	values := taskDagNodeValues(row)
	values = append(values, prev)
	return values, nil
}

// appendRunEvent mirrors the F1.5 AppendTaskDagRunEvent SQL: find the running
// run for dag_key, concat events || $2::jsonb; apply the 50-event ring trim
// (port-unification batch); return run_key. Returns pgx.ErrNoRows when no
// running run matches (the store treats that as a soft miss).
//
// The fake parses+re-serialises the events array as a real JSON []any (rather
// than the previous raw-bytes concat trick) so that the ring trim semantics
// match production: when length > 50 keep only the last 50 entries.
func (db *fakeTaskDAGDB) appendRunEvent(args ...any) ([]any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("append event args len = %d, want 2", len(args))
	}
	dagKey, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("dag key arg = %T", args[0])
	}
	payload, ok := args[1].([]byte)
	if !ok {
		return nil, fmt.Errorf("payload arg = %T", args[1])
	}
	// Decode new event payload as opaque json value (object).
	var newEvent any
	if err := json.Unmarshal(payload, &newEvent); err != nil {
		return nil, fmt.Errorf("decode event payload: %w", err)
	}
	for runKey, run := range db.runs {
		if run.DagKey != dagKey || run.Status != "running" {
			continue
		}
		// Decode existing events array; missing/empty → start with [].
		var arr []any
		if len(run.Events) > 0 {
			if err := json.Unmarshal(run.Events, &arr); err != nil {
				// Tolerate legacy raw-bytes garbage by resetting to empty array;
				// production never produces such state.
				arr = nil
			}
		}
		arr = append(arr, newEvent)
		// Ring trim: keep last 50 entries. Mirrors the SQL CASE branch.
		const ringCap = 50
		if len(arr) > ringCap {
			arr = arr[len(arr)-ringCap:]
		}
		encoded, err := json.Marshal(arr)
		if err != nil {
			return nil, fmt.Errorf("encode events array: %w", err)
		}
		run.Events = encoded
		run.UpdatedAt = timestamptzValue(db.now)
		db.runs[runKey] = run
		return []any{run.RunKey}, nil
	}
	return nil, pgx.ErrNoRows
}

func firstLine(sql string) string {
	if idx := strings.IndexByte(sql, '\n'); idx >= 0 {
		return sql[:idx]
	}
	return sql
}
