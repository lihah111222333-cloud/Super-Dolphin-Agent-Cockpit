package taskdag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

// DAG v2 F1.5 / ADR-009: RecordNodeSpawn 是 nodeexec.AgentExecutor 在 child
// agent thread spawn 成功后调用的入口。
//
// 同事务两步：
//  1. UpdateTaskDagNodeSpawningThread 覆盖 task_dag_nodes.spawning_thread_id
//     并通过 CTE 拿出旧值（previous_spawning_thread_id）；
//  2. 旧值非空且 != 新值 → AppendTaskDagRunEvent 把
//     {kind:"node_spawn", node_key, prev_thread_id, thread_id, ts} append 到
//     该 dag_key 当前 running run 的 events jsonb 数组。无 running run 时返
//     0 行：result.AppendedEvent=false，不报错（spawn 历史是辅助审计，缺失
//     不应让 spawn 路径整体失败）。
//
// 不在事务内 finalize run，因为 spawn 是 run.status 'running' 阶段事件，不会
// 影响终态判定（终态由 F6.2 maybeFinalizeRunTx 负责）。
//
// RecordNodeSpawn is the entry point invoked by nodeexec.AgentExecutor after
// a child agent thread is launched successfully (F1.5 / ADR-009). It runs in
// a single PG transaction:
//   - Overwrite task_dag_nodes.spawning_thread_id (CTE returns the previous
//     value alongside the updated row).
//   - When the previous thread id is non-empty and differs from the new one,
//     append a `node_spawn` event into the matching running run's events
//     jsonb array. A missing running run is treated as a soft miss
//     (AppendedEvent=false), never an error, since the spawn history is an
//     audit aid and absence should not fail the launch path.
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

	var result RecordNodeSpawnResult
	err := sqlc.WithTxOrReuse(ctx, s.q, func(txq *sqlc.Queries) error {
		// 1) UPDATE node + CTE capture previous value.
		row, updateErr := txq.UpdateTaskDagNodeSpawningThread(ctx, sqlc.UpdateTaskDagNodeSpawningThreadParams{
			SpawningThreadID: pgtype.Text{String: threadID, Valid: true},
			DagKey:           dagKey,
			NodeKey:          nodeKey,
		})
		if updateErr != nil {
			return updateErr
		}
		result.Node = nodeFromSpawnRow(row)
		if row.PreviousSpawningThreadID.Valid {
			result.PreviousThreadID = row.PreviousSpawningThreadID.String
		}

		// 2) Append node_spawn event only on retry overwrite (prev != new).
		//    First spawn (prev empty) skips the event to keep events lean.
		if result.PreviousThreadID != "" && result.PreviousThreadID != threadID {
			payload, marshalErr := json.Marshal(nodeSpawnEvent{
				Kind:           "node_spawn",
				NodeKey:        nodeKey,
				PrevThreadID:   result.PreviousThreadID,
				ThreadID:       threadID,
				TS:             time.Now().UTC().Format(time.RFC3339Nano),
			})
			if marshalErr != nil {
				return fmt.Errorf("marshal node_spawn event: %w", marshalErr)
			}
			runKey, appendErr := txq.AppendTaskDagRunEvent(ctx, sqlc.AppendTaskDagRunEventParams{
				DagKey:  dagKey,
				Column2: payload,
			})
			if appendErr != nil {
				// pgx.ErrNoRows here means "no running run for dag_key" — the
				// spawn history append is a soft miss, not a hard failure.
				if errors.Is(appendErr, pgx.ErrNoRows) {
					return nil
				}
				return appendErr
			}
			result.AppendedEvent = true
			result.RunKey = runKey
		}
		return nil
	})
	if err != nil {
		return nil, wrapTaskDAGError(err, "record_node_spawn", "task_dag_node")
	}
	return &result, nil
}

// nodeSpawnEvent 是写入 task_dag_runs.events jsonb 数组的事件载荷。kind 固定
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
