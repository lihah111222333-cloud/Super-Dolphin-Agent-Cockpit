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

// RecordNodeSpawn 是 nodeexec.AgentExecutor 在子 agent thread 创建成功后的写回入口。
// 同事务两步：
//  1. UpdateTaskDagNodeSpawningThread 覆盖 task_dag_nodes.spawning_thread_id
//     并通过 CTE 拿出旧值（previous_spawning_thread_id）；
//  2. 旧值非空且 != 新值 → appendTaskDagRunEventTx 把
//     {kind:"node_spawn", node_key, prev_thread_id, thread_id, ts} append 到
//     该 run 的 events JSON 数组。无 running run 时硬报错，
//     避免重试历史缺失被静默吞掉。
//
// 不在事务内 finalize run，因为 spawn 是 run.status 'running' 阶段事件，不会
// 影响终态判定；终态推进仍由 CompleteNode 路径负责。
func (s *store) RecordNodeSpawn(ctx context.Context, input RecordNodeSpawnInput) (*RecordNodeSpawnResult, error) {
	dagKey := strings.TrimSpace(input.DagKey)
	nodeKey := strings.TrimSpace(input.NodeKey)
	threadID := strings.TrimSpace(input.ThreadID)
	if dagKey == "" || nodeKey == "" {
		return nil, fmt.Errorf("record_node_spawn: dag_key and node_key required")
	}
	if threadID == "" {
		// thread_id 缺失通常意味着 launcher 返回了空结果；拒绝写入可避免擦掉已有有效值。
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

// recordNodeSpawnTx 是事务体拆出的两步逻辑：UPDATE 节点 + 重试改绑时 append events。
// 拆为独立函数是为了让入口只表达参数校验和事务边界，实际写入顺序集中在这里维护。
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
	// 首次 spawn 或幂等重试不追加事件，只有真实改绑才记录重试历史。
	if result.PreviousThreadID == "" || result.PreviousThreadID == threadID {
		return nil
	}
	return appendNodeSpawnEvent(ctx, txq, dagKey, nodeKey, runID, threadID, result)
}

// appendNodeSpawnEvent 封装「构造 payload + appendTaskDagRunEventTx」。
// sql.ErrNoRows（dag_key 下无 running run）必须传错上去。
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
type nodeSpawnEvent struct {
	Kind         string `json:"kind"`
	NodeKey      string `json:"node_key"`
	PrevThreadID string `json:"prev_thread_id"`
	ThreadID     string `json:"thread_id"`
	TS           string `json:"ts"`
}

// nodeFromSpawnRow 把更新 spawning_thread_id 的 CTE 返回行投影成 Node。
// 该行包含标准节点列和额外 previous_spawning_thread_id，投影时只复制 Node 需要的列。
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
