package taskdag

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlctx"
)

// DAG v2 F1.5 / ADR-009: RecordNodeSpawn 是 nodeexec.AgentExecutor 在 child
// agent thread spawn 成功后调用的入口。
//
// 同事务两步：
//  1. UpdateTaskDagNodeSpawningThread 覆盖 task_dag_nodes.spawning_thread_id
//     并通过 CTE 拿出旧值（previous_spawning_thread_id）；
//  2. 旧值非空且 != 新值 → appendTaskDagRunEventTx 把
//     {kind:"node_spawn", node_key, prev_thread_id, thread_id, ts} append 到
//     该 run 的 events JSON 数组。无 running run 时硬报错，
//     避免重试历史缺失被静默吞掉。
//
// 不在事务内 finalize run，因为 spawn 是 run.status 'running' 阶段事件，不会
// 影响终态判定（终态由 F6.2 maybeFinalizeRunTx 负责）。
//
// RecordNodeSpawn is the entry point invoked by nodeexec.AgentExecutor after
// a child agent thread is launched successfully (F1.5 / ADR-009). It runs in
// a single SQLite BEGIN IMMEDIATE transaction:
//   - Overwrite task_dag_nodes.spawning_thread_id (CTE returns the previous
//     value alongside the updated row).
//   - When the previous thread id is non-empty and differs from the new one,
//     append a `node_spawn` event into the matching running run's events
//     JSON array. A missing running run is a hard error so retry history
//     cannot disappear silently.
//
// Finalization (F6.2) is deliberately not invoked here: spawn happens while
// the run is still 'running', so terminal-status promotion stays the
// CompleteNode path's responsibility.
func (s *store) RecordNodeSpawn(ctx context.Context, input RecordNodeSpawnInput) (*RecordNodeSpawnResult, error) {
	dagKey := strings.TrimSpace(input.DagKey)
	nodeKey := strings.TrimSpace(input.NodeKey)
	threadID := strings.TrimSpace(input.ThreadID)
	if dagKey == "" || nodeKey == "" {
		return nil, fmt.Errorf("record_node_spawn: dag_key and node_key required")
	}
	if threadID == "" {
		// Fail-fast: a missing thread_id likely means the launcher returned
		// nil/empty; overwriting with empty would erase a valid prior value.
		return nil, fmt.Errorf("record_node_spawn: thread_id required (refusing to overwrite with empty)")
	}
	if input.RunID <= 0 {
		return nil, fmt.Errorf("record_node_spawn: run_id required")
	}

	var result RecordNodeSpawnResult
	err := sqlctx.WithImmediateTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, _ sqlc.DBTX) error {
		return recordNodeSpawnTx(ctx, txq, dagKey, nodeKey, input.RunID, threadID, &result)
	})
	if err != nil {
		return nil, wrapTaskDAGError(err, "record_node_spawn", "task_dag_node")
	}
	return &result, nil
}

// recordNodeSpawnTx 是事务体拆出的两步逻辑：UPDATE 节点 + （重试时）append events。
// 拆为独立函数是为了把 RecordNodeSpawn 本体的圈复杂度压到代码守卫上限（§10）。
//
// recordNodeSpawnTx is the two-step transaction body (UPDATE the node, then
// optionally append a node_spawn event on retry). Extracted as a free
// function so the public RecordNodeSpawn stays under the cyclomatic
// complexity guard (10).
func recordNodeSpawnTx(ctx context.Context, txq *sqlc.Queries, dagKey, nodeKey string, runID int64, threadID string, result *RecordNodeSpawnResult) error {
	current, err := txq.GetTaskDagRunNodeForUpdate(ctx, sqlc.GetTaskDagRunNodeForUpdateParams{
		DagKey:  dagKey,
		NodeKey: nodeKey,
		RunID:   int64Ptr(runID),
	})
	if err != nil {
		return err
	}
	previousThreadID := ""
	if current.SpawningThreadID != nil {
		previousThreadID = strings.TrimSpace(*current.SpawningThreadID)
	}
	row, err := txq.UpdateTaskDagNodeSpawningThread(ctx, sqlc.UpdateTaskDagNodeSpawningThreadParams{
		SpawningThreadID: sqlc.TextValuePtr(&threadID),
		DagKey:           dagKey,
		NodeKey:          nodeKey,
		RunID:            int64Ptr(runID),
	})
	if err != nil {
		return err
	}
	result.Node = nodeFromSpawnRow(row)
	result.PreviousThreadID = previousThreadID
	// First spawn (prev empty) or idempotent retry (prev == new) keeps events lean.
	if result.PreviousThreadID == "" || result.PreviousThreadID == threadID {
		return nil
	}
	return appendNodeSpawnEvent(ctx, txq, dagKey, nodeKey, runID, threadID, result)
}

// appendNodeSpawnEvent 封装「构造 payload + appendTaskDagRunEventTx」。
// sql.ErrNoRows（dag_key 下无 running run）必须传错上去。
//
// appendNodeSpawnEvent wraps the marshal + append path so
// recordNodeSpawnTx stays linear and under the complexity ceiling.
func appendNodeSpawnEvent(ctx context.Context, txq *sqlc.Queries, dagKey, nodeKey string, runID int64, threadID string, result *RecordNodeSpawnResult) error {
	payload, err := json.Marshal(nodeSpawnEvent{
		Kind:         "node_spawn",
		NodeKey:      nodeKey,
		PrevThreadID: result.PreviousThreadID,
		ThreadID:     threadID,
		TS:           time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("marshal node_spawn event: %w", err)
	}
	runKey, err := appendTaskDagRunEventTx(ctx, txq, dagKey, runID, payload)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("append node_spawn event: running run not found for dag %q run %d: %w", dagKey, runID, err)
		}
		return err
	}
	result.AppendedEvent = true
	result.RunKey = runKey
	return nil
}

// nodeSpawnEvent 是写入 task_dag_runs.events JSON 数组的事件载荷。kind 固定
// "node_spawn"；TS 用 RFC3339Nano 字符串方便 UI 直接渲染。
//
// nodeSpawnEvent is the JSON payload written into task_dag_runs.events. The
// kind discriminator stays "node_spawn"; TS uses RFC3339Nano so the UI can
// render it directly without re-parsing.
type nodeSpawnEvent struct {
	Kind         string `json:"kind"`
	NodeKey      string `json:"node_key"`
	PrevThreadID string `json:"prev_thread_id"`
	ThreadID     string `json:"thread_id"`
	TS           string `json:"ts"`
}

// nodeFromSpawnRow projects the CTE row back into the domain Node type. The
// CTE row carries the same 19 node columns as fromNode plus the extra
// previous_spawning_thread_id, so this projection is a thin shim that reuses
// fromNode's shape by manually copying fields.
func nodeFromSpawnRow(row sqlc.UpdateTaskDagNodeSpawningThreadRow) *Node {
	n := Node{
		ID:               row.ID,
		DagKey:           row.DagKey,
		NodeKey:          row.NodeKey,
		RunID:            sqlc.Int8Ptr(row.RunID),
		Title:            row.Title,
		NodeType:         row.NodeType,
		AssignedTo:       row.AssignedTo,
		DependsOn:        row.DependsOn,
		Status:           row.Status,
		CommandRef:       row.CommandRef,
		Config:           row.Config,
		Result:           row.Result,
		StartedAt:        timestampPtr(row.StartedAt),
		FinishedAt:       timestampPtr(row.FinishedAt),
		CreatedAt:        timeValue(row.CreatedAt),
		UpdatedAt:        timeValue(row.UpdatedAt),
		ActiveTurnID:     sqlc.TextPtr(row.ActiveTurnID),
		ActiveWakeupID:   sqlc.Int8Ptr(row.ActiveWakeupID),
		LastEventAt:      timestampPtr(row.LastEventAt),
		SpawningThreadID: sqlc.TextPtr(row.SpawningThreadID),
	}
	return &n
}
