package taskdag

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
)

// RecordNodeSpawn 是 nodeexec.AgentExecutor 在子 agent thread 创建成功后的写回入口。
// 同事务执行 CAS 写回：只有空 spawning_thread_id 或同一 threadID 的幂等重试可以写入；
// 已存在不同 threadID 时返回 conflict，避免覆盖仍在运行的 child 归属。
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
	fence, fenced, err := resolveWakeupFence(ctx, input.WakeupID, input.WakeupFence)
	if err != nil {
		return nil, wrapTaskDAGError(err, "record_node_spawn", "task_dag_node")
	}

	var result RecordNodeSpawnResult
	err = sqlctx.WithImmediateTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, _ sqlc.DBTX) error {
		if fenced {
			if err := validateWakeupFenceTx(ctx, txq, fence, dagKey, nodeKey, input.RunID); err != nil {
				return err
			}
		}
		return recordNodeSpawnTx(ctx, txq, dagKey, nodeKey, input.RunID, threadID, &result)
	})
	if err != nil {
		return nil, wrapTaskDAGError(err, "record_node_spawn", "task_dag_node")
	}
	return &result, nil
}

// resolveWakeupFence 合并显式输入和 ctx 中的 wakeup fence；无 wakeup_id 时保持非 dispatch 兼容路径。
func resolveWakeupFence(ctx context.Context, wakeupID int64, input WakeupFence) (WakeupFence, bool, error) {
	if wakeupID > 0 && input.WakeupID == 0 {
		input.WakeupID = wakeupID
	}
	if ctxFence, ok := WakeupFenceFromContext(ctx); ok && ctxFence.WakeupID > 0 {
		switch input.WakeupID {
		case 0:
			input = ctxFence
		case ctxFence.WakeupID:
			input = mergeWakeupFence(input, ctxFence)
		}
	}
	if input.WakeupID == 0 {
		return WakeupFence{}, false, nil
	}
	if err := validateWakeupFenceFields(input); err != nil {
		return WakeupFence{}, true, err
	}
	return input, true, nil
}

func mergeWakeupFence(input, fallback WakeupFence) WakeupFence {
	if input.WakeupAttempt == 0 {
		input.WakeupAttempt = fallback.WakeupAttempt
	}
	if strings.TrimSpace(input.ClaimedBy) == "" {
		input.ClaimedBy = fallback.ClaimedBy
	}
	if input.ClaimedAt.IsZero() {
		input.ClaimedAt = fallback.ClaimedAt
	}
	if input.LeaseExpiresAt.IsZero() {
		input.LeaseExpiresAt = fallback.LeaseExpiresAt
	}
	return input
}

// validateWakeupFenceFields 校验 CAS 所需的 wakeup lease 字段必须完整。
func validateWakeupFenceFields(fence WakeupFence) error {
	switch {
	case fence.WakeupID <= 0:
		return errors.New("wakeup fence: wakeup_id required")
	case fence.WakeupAttempt <= 0:
		return errors.New("wakeup fence: attempt required")
	case strings.TrimSpace(fence.ClaimedBy) == "":
		return errors.New("wakeup fence: claimed_by required")
	case fence.ClaimedAt.IsZero():
		return errors.New("wakeup fence: claimed_at required")
	case fence.LeaseExpiresAt.IsZero():
		return errors.New("wakeup fence: lease_expires_at required")
	default:
		return nil
	}
}

// validateWakeupFenceTx 在写事务内确认 wakeup lease 仍由当前 dispatcher 持有。
func validateWakeupFenceTx(ctx context.Context, txq *sqlc.Queries, fence WakeupFence, dagKey, nodeKey string, runID int64) error {
	row, err := txq.GetTaskDagWakeup(ctx, sqlc.GetTaskDagWakeupParams{ID: fence.WakeupID})
	if err != nil {
		return err
	}
	if err := validateWakeupFenceRowIdentity(row, fence, dagKey, nodeKey, runID); err != nil {
		return err
	}
	if err := validateWakeupFenceRowLease(row, fence); err != nil {
		return err
	}
	if fence.LeaseExpiresAt.Before(time.Now().UTC()) {
		return fmt.Errorf("wakeup fence expired: wakeup_id=%d lease_expires_at=%s", fence.WakeupID, fence.LeaseExpiresAt.UTC().Format(time.RFC3339Nano))
	}
	return nil
}

// validateWakeupFenceRowIdentity 校验 wakeup 与当前 runtime node 三元组仍然一致。
func validateWakeupFenceRowIdentity(row sqlc.GetTaskDagWakeupRow, fence WakeupFence, dagKey, nodeKey string, runID int64) error {
	if row.Status != "dispatching" {
		return wakeupFenceMismatch(fence, dagKey, nodeKey, runID)
	}
	if row.RunID == nil || *row.RunID != runID {
		return wakeupFenceMismatch(fence, dagKey, nodeKey, runID)
	}
	if row.DagKey != dagKey || row.NodeKey != nodeKey {
		return wakeupFenceMismatch(fence, dagKey, nodeKey, runID)
	}
	return nil
}

// validateWakeupFenceRowLease 校验 claim owner、attempt 和 lease 时间戳仍匹配。
func validateWakeupFenceRowLease(row sqlc.GetTaskDagWakeupRow, fence WakeupFence) error {
	claimedAt := timestampValue(fence.ClaimedAt)
	leaseExpiresAt := timestampValue(fence.LeaseExpiresAt)
	if row.AttemptCount != int64(fence.WakeupAttempt) {
		return wakeupFenceLeaseMismatch(fence)
	}
	if row.ClaimedAt == nil || *row.ClaimedAt != *claimedAt {
		return wakeupFenceLeaseMismatch(fence)
	}
	if row.ClaimedBy != strings.TrimSpace(fence.ClaimedBy) {
		return wakeupFenceLeaseMismatch(fence)
	}
	if row.LeaseExpiresAt == nil || *row.LeaseExpiresAt != *leaseExpiresAt {
		return wakeupFenceLeaseMismatch(fence)
	}
	return nil
}

func wakeupFenceMismatch(fence WakeupFence, dagKey, nodeKey string, runID int64) error {
	return fmt.Errorf("wakeup fence mismatch: wakeup_id=%d dag_key=%s node_key=%s run_id=%d", fence.WakeupID, dagKey, nodeKey, runID)
}

func wakeupFenceLeaseMismatch(fence WakeupFence) error {
	return fmt.Errorf("wakeup fence mismatch: wakeup_id=%d", fence.WakeupID)
}

// recordNodeSpawnTx 是事务体拆出的 CAS 逻辑：UPDATE 节点 + 幂等重试不追加事件。
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
	if previousThreadID != "" && previousThreadID != threadID {
		return fmt.Errorf("%w: spawning_thread_id for dag=%s node=%s run_id=%d already bound to %q, refusing overwrite with %q", platformdb.ErrConflict, dagKey, nodeKey, runID, previousThreadID, threadID)
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
	return nil
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
		Reads:            nodeStringSlice(row.Reads),
		Writes:           nodeStringSlice(row.Writes),
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
