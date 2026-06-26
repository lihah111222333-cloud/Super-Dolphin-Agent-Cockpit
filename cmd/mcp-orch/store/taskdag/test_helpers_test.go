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
	// runs 模拟 task_dag_runs 表，按 run_key 存放行，finalize 分支会在所有节点终态后更新 run.status。
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

// Query 复刻 fake DB 里会返回多行的 SQL 分支；单节点推进只允许 pending -> ready，否则返回 0 行。
func (db *fakeTaskDAGDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	rows, err := db.queryRowsLocked(sql, args...)
	if err != nil {
		return nil, err
	}
	return &stubTaskDAGRows{rows: rows}, nil
}

// QueryRow 复刻 fake DB 里只返回一行的 SQL 分支；finalize 会按 failed > cancelled > succeeded 决定最终 run 状态。
// 若还有非终态节点或 dag_key 下没有 running run，则返回空结果以匹配 store 的软未命中路径。
func (db *fakeTaskDAGDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	db.mu.Lock()
	defer db.mu.Unlock()
	values, err := db.queryRowValuesLocked(sql, args...)
	return stubTaskDAGRow{values: values, err: err}
}

// updateTag 构造 UPDATE 结果行数；传入错误时保留原错误，便于 fake SQL 分支复刻生产未命中路径。
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

// updateNodeSpawningThread 复刻更新 spawning_thread_id 的 CTE：先捕获旧值，再更新可运行节点。
// 返回列比完整 TaskDagNode 更窄，顺序必须保持与 sqlc Scan 调用一致。
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

// appendRunEvent 复刻运行中 run 的事件追加逻辑，并应用 50 条环形裁剪。
// fake 会解析并重新序列化 JSON 数组，避免字节拼接掩盖生产语义差异。
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
