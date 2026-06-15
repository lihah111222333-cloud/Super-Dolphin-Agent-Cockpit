//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"fmt"
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
	ops     []string
	locks   map[string]bool
	// F6.2: runs 鐢ㄤ簬妯℃嫙 task_dag_runs 涓€琛岋紝閿槸 run_key锛沠inalize SQL 鎷︽埅浼氳鍐欏畠銆?
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
		locks:   make(map[string]bool),
	}
}

func (db *fakeTaskDAGDB) advance(delta time.Duration) { db.now = db.now.Add(delta) }

func (db *fakeTaskDAGDB) Begin(context.Context) (pgx.Tx, error) {
	db.mu.Lock()
	working := db.cloneLocked()
	beforeFailNonTerminal := db.beforeFailNonTerminal
	defer db.mu.Unlock()
	if beforeFailNonTerminal != nil {
		parent := db
		working.beforeFailNonTerminal = func(dagKey, nodeKey string) {
			beforeFailNonTerminal(dagKey, nodeKey)
			parent.mu.Lock()
			refreshed := make(map[string]sqlc.TaskDagNode)
			for key, row := range parent.nodes {
				if row.DagKey == dagKey && row.NodeKey == nodeKey {
					refreshed[key] = cloneTaskDagNode(row)
				}
			}
			parent.mu.Unlock()
			for key, row := range working.nodes {
				if row.DagKey == dagKey && row.NodeKey == nodeKey {
					delete(working.nodes, key)
				}
			}
			for key, row := range refreshed {
				working.nodes[key] = row
			}
		}
	}
	return &fakeTaskDAGTx{
		parent:  db,
		working: working,
	}, nil
}

func (db *fakeTaskDAGDB) clone() *fakeTaskDAGDB {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.cloneLocked()
}

func (db *fakeTaskDAGDB) cloneLocked() *fakeTaskDAGDB {
	cloned := newFakeTaskDAGDB(db.now)
	cloned.wakeupSeq = db.wakeupSeq
	cloned.runSeq = db.runSeq
	for key, row := range db.dags {
		row.Metadata = cloneBytes(row.Metadata)
		cloned.dags[key] = row
	}
	for key, row := range db.wakeups {
		row.PromptPayload = cloneBytes(row.PromptPayload)
		cloned.wakeups[key] = row
	}
	for key, row := range db.nodes {
		cloned.nodes[key] = cloneTaskDagNode(row)
	}
	for key, row := range db.runs {
		row.Events = cloneBytes(row.Events)
		row.Metadata = cloneBytes(row.Metadata)
		cloned.runs[key] = row
	}
	cloned.ops = append([]string(nil), db.ops...)
	return cloned
}

func (db *fakeTaskDAGDB) replaceLocked(snapshot *fakeTaskDAGDB) {
	db.now = snapshot.now
	db.dags = snapshot.dags
	db.wakeups = snapshot.wakeups
	db.nodes = snapshot.nodes
	db.runs = snapshot.runs
	db.ops = snapshot.ops
	db.wakeupSeq = snapshot.wakeupSeq
	db.runSeq = snapshot.runSeq
}

func cloneTaskDagNode(row sqlc.TaskDagNode) sqlc.TaskDagNode {
	row.DependsOn = cloneBytes(row.DependsOn)
	row.Config = cloneBytes(row.Config)
	row.Result = cloneBytes(row.Result)
	return row
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value...)
}

type fakeTaskDAGTx struct {
	parent  *fakeTaskDAGDB
	working *fakeTaskDAGDB
	closed  bool
}

func (*fakeTaskDAGTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, fmt.Errorf("fakeTaskDAGTx: nested transaction not implemented")
}

func (tx *fakeTaskDAGTx) Commit(context.Context) error {
	if tx.closed {
		return pgx.ErrTxClosed
	}
	snapshot := tx.working.clone()
	tx.parent.mu.Lock()
	defer tx.parent.mu.Unlock()
	tx.parent.replaceLocked(snapshot)
	tx.closed = true
	return nil
}

func (tx *fakeTaskDAGTx) Rollback(context.Context) error {
	if tx.closed {
		return pgx.ErrTxClosed
	}
	tx.closed = true
	return nil
}

func (*fakeTaskDAGTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, fmt.Errorf("fakeTaskDAGTx: copyfrom not implemented")
}

func (*fakeTaskDAGTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }

func (*fakeTaskDAGTx) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }

func (*fakeTaskDAGTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, fmt.Errorf("fakeTaskDAGTx: prepare not implemented")
}

func (tx *fakeTaskDAGTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if tx.closed {
		return pgconn.CommandTag{}, pgx.ErrTxClosed
	}
	return tx.working.Exec(ctx, sql, args...)
}

func (tx *fakeTaskDAGTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if tx.closed {
		return nil, pgx.ErrTxClosed
	}
	return tx.working.Query(ctx, sql, args...)
}

func (tx *fakeTaskDAGTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if tx.closed {
		return stubTaskDAGRow{err: pgx.ErrTxClosed}
	}
	return tx.working.QueryRow(ctx, sql, args...)
}

func (*fakeTaskDAGTx) Conn() *pgx.Conn { return nil }

func (db *fakeTaskDAGDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.execCommandLocked(sql, args...)
}

// promoteSingleNodePendingToReady 澶嶇幇 F6.3 PromoteSingleNodePendingToReady SQL
// 鐨勮涔夛細浠呭綋 node 杩樺湪 pending 鏃舵墠鎺ㄨ繘鍒?ready锛屽苟杩斿洖鍙楀奖鍝嶈鏁般€?
// promoteSingleNodePendingToReady mirrors the F6.3 SQL: only flip when the
// node row is still in 'pending', otherwise return 0 affected rows.
func (db *fakeTaskDAGDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	rows, err := db.queryRowsLocked(sql, args...)
	if err != nil {
		return nil, err
	}
	return &stubTaskDAGRows{rows: rows}, nil
}

// finalizeRunIfAllNodesTerminal 澶嶇幇 F6.2 SQL 鐨勮涔夛細鍦ㄥ悓涓€涓?fake DB 涓婃壂
// dag_key 涓嬭妭鐐圭姸鎬侊紝鎸変紭鍏堢骇 (failed > cancelled > succeeded) 鍐冲畾
// final_status锛涜妭鐐硅繕鏈夐潪缁堟€佹垨 dag_key 涓嬫棤 running run 鏃惰繑鍥?0 琛屻€?
//
// finalizeRunIfAllNodesTerminal mirrors the F6.2 finalize SQL semantics inside
// the fake DB. Empty result rows mean either some nodes are still non-terminal
// or no 'running' run exists for dag_key.
func (db *fakeTaskDAGDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	db.mu.Lock()
	defer db.mu.Unlock()
	values, err := db.queryRowValuesLocked(sql, args...)
	return stubTaskDAGRow{values: values, err: err}
}

// lookupNodesBySpawningThread mirrors the ADR-017 搂2.2 reverse-lookup query:
// SELECT * FROM task_dag_nodes WHERE spawning_thread_id = $1 AND
// spawning_thread_id IS NOT NULL ORDER BY updated_at DESC, id DESC.
// updateRunningNodeStatus mirrors the W4-fence UpdateRunningTaskDagNodeStatus
// SQL: only flip when current status is in ('pending','ready') (matches the
// production fence post W4 fix). Returns pgx.ErrNoRows otherwise so the store
// surfaces the same "not in fence" path as production.
func updateTag(count int64, err error) (pgconn.CommandTag, error) {
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", count)), nil
}

func dagNodeKey(dagKey, nodeKey string) string { return dagKey + "\x00" + nodeKey }

func dagRunNodeKey(dagKey, nodeKey string, runID int64) string {
	return fmt.Sprintf("%s\x00%d\x00%s", dagKey, runID, nodeKey)
}

func dagNodeLookupKey(dagKey, nodeKey string, runID int64) string {
	if runID <= 0 {
		return dagRunNodeKey(dagKey, nodeKey, runID)
	}
	return dagRunNodeKey(dagKey, nodeKey, runID)
}

func fakeRunID(row sqlc.TaskDagNode) int64 {
	if !row.RunID.Valid {
		return 0
	}
	return row.RunID.Int64
}

func fakeWakeupRunID(row sqlc.TaskDagWakeup) int64 {
	if !row.RunID.Valid {
		return 0
	}
	return row.RunID.Int64
}

func nilIfZero(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}

// updateNodeSpawningThread mirrors the F1.5 CTE SQL: capture previous value
// then UPDATE. The CTE row shape is intentionally narrower than TaskDagNode;
// the order must match
// task_dag_node_spawning_thread.sql.go's Scan call so stubTaskDAGRow can
// assign cleanly.
func (db *fakeTaskDAGDB) updateNodeSpawningThread(args ...any) ([]any, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("spawning thread args len = %d, want 4", len(args))
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
	runID, err := fakeInt8Arg(args, 3, "run id")
	if err != nil {
		return nil, err
	}
	key := dagNodeLookupKey(dagKey, nodeKey, runID)
	row, ok := db.nodes[key]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	switch row.Status {
	case "done", "failed", "cancelled", "skipped":
		return nil, pgx.ErrNoRows
	}
	prev := row.SpawningThreadID
	row.SpawningThreadID = spawningThread
	row.UpdatedAt = timestamptzValue(db.now)
	db.nodes[key] = row
	return []any{
		row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType,
		row.AssignedTo, append([]byte(nil), row.DependsOn...), row.Status,
		row.CommandRef, append([]byte(nil), row.Config...), append([]byte(nil), row.Result...),
		row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt,
		row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt,
		row.SpawningThreadID, prev,
	}, nil
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
	if err := requireFakeTaskDAGArgs(args, 3, "append event",
		fakeTaskDAGTypedArg[string](0, "dag key"),
		fakeTaskDAGTypedArg[[]byte](1, "payload"),
		fakeTaskDAGInt8Arg(2, "run id")); err != nil {
		return nil, err
	}
	dagKey := args[0].(string)
	payload := args[1].([]byte)
	runID, err := fakeInt8Arg(args, 2, "run id")
	if err != nil {
		return nil, err
	}
	newEvent, err := decodeRunEventPayload(payload)
	if err != nil {
		return nil, err
	}
	for runKey, run := range db.runs {
		if !isMatchingRunningRun(run, dagKey, runID) {
			continue
		}
		updated, err := appendRunEventPayload(run, newEvent)
		if err != nil {
			return nil, err
		}
		updated.UpdatedAt = timestamptzValue(db.now)
		db.runs[runKey] = updated
		return []any{updated.RunKey}, nil
	}
	return nil, pgx.ErrNoRows
}

func firstLine(sql string) string {
	if idx := strings.IndexByte(sql, '\n'); idx >= 0 {
		return sql[:idx]
	}
	return sql
}
