package taskdag

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/sqlctx"
)

// ErrDAGDeleteActiveRun identifies a taskdag error condition.
var ErrDAGDeleteActiveRun = errors.New("task_dag: delete blocked by active running run")

type store struct {
	db sqlc.DBTX
	q  *sqlc.Queries
}

// NewStore 创建存储。
func NewStore(db sqlc.DBTX) Store { return &store{db: db, q: sqlc.New(db)} }

func requireRuntimeRunID(op string, runID int64) error {
	if runID <= 0 {
		return fmt.Errorf("%s: run_id required", op)
	}
	return nil
}

// WithTx 在 SQLite IMMEDIATE 写事务中重新绑定 sqlc 查询集。
// 旧 PostgreSQL 行锁读路径由 BEGIN IMMEDIATE 加显式 CAS 写谓词串行化。
func (s *store) WithTx(ctx context.Context, fn func(txStore DAGMutationStore) error) error {
	return wrapTaskDAGError(sqlctx.WithImmediateTx(ctx, s.db, s.q, func(txq *sqlc.Queries, tx sqlc.DBTX) error {
		return fn(&store{db: tx, q: txq})
	}), "with_tx", "task_dag")
}

// UpsertDAG 处理upsertDAG。
func (s *store) UpsertDAG(ctx context.Context, dag DAG) (*DAG, error) {
	return queryOneWrite(ctx, func() (sqlc.UpsertTaskDagRow, error) {
		return s.q.UpsertTaskDag(ctx, sqlc.UpsertTaskDagParams{
			DagKey:      dag.DagKey,
			Title:       dag.Title,
			Description: dag.Description,
			Status:      dag.Status,
			CreatedBy:   dag.CreatedBy,
			Metadata:    dag.Metadata,
		})
	}, "upsert", "task_dag", fromDAGUpsertRow)
}

// ListDAGs 列出dags。
func (s *store) ListDAGs(ctx context.Context, filter ListDAGsFilter) ([]DAG, error) {
	return queryMany(func() ([]sqlc.ListTaskDagsRow, error) {
		return s.q.ListTaskDags(ctx, sqlc.ListTaskDagsParams{
			StatusFilter: filter.Status,
			Keyword:      filter.Keyword,
			LimitCount:   int64(filter.Limit),
		})
	}, "list", "task_dag", fromDAGListRow)
}

// GetDAG 读取DAG。
func (s *store) GetDAG(ctx context.Context, dagKey string) (*DAG, error) {
	return queryOne(func() (sqlc.GetTaskDagRow, error) {
		return s.q.GetTaskDag(ctx, sqlc.GetTaskDagParams{DagKey: dagKey})
	}, "get", "task_dag", fromDAGGetRow)
}

// DeleteDAG 删除DAG。
func (s *store) DeleteDAG(ctx context.Context, dagKey string) (int64, error) {
	key := strings.TrimSpace(dagKey)
	if key == "" {
		return 0, errors.New("delete task_dag: dag_key is required")
	}
	var rows int64
	err := sqlctx.WithImmediateTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, txdb sqlc.DBTX) error {
		txStore := &store{db: txdb, q: txq}
		if _, err := txStore.lockDAGForDelete(ctx, key); err != nil {
			return err
		}
		active, err := txStore.CountRunningRunsByDagKey(ctx, key)
		if err != nil {
			return err
		}
		if active > 0 {
			return ErrDAGDeleteActiveRun
		}
		if err := txStore.deleteDAGDependents(ctx, key); err != nil {
			return err
		}
		deleted, err := txStore.deleteDAGRow(ctx, key)
		if err != nil {
			return err
		}
		rows = deleted
		return nil
	})
	return rows, wrapTaskDAGError(err, "delete", "task_dag")
}

// UpsertNode 处理upsert节点。
func (s *store) UpsertNode(ctx context.Context, node Node) (*Node, error) {
	return queryOneWrite(ctx, func() (sqlc.UpsertTaskDagNodeRow, error) {
		return s.q.UpsertTaskDagNode(ctx, sqlc.UpsertTaskDagNodeParams{
			DagKey:     node.DagKey,
			NodeKey:    node.NodeKey,
			Title:      node.Title,
			NodeType:   node.NodeType,
			AssignedTo: node.AssignedTo,
			DependsOn:  node.DependsOn,
			CommandRef: node.CommandRef,
			Config:     node.Config,
		})
	}, "upsert", "task_dag_node", fromNodeUpsertRow)
}

// PatchNodeConfigIfUnchanged 处理补丁节点配置ifunchanged。
func (s *store) PatchNodeConfigIfUnchanged(ctx context.Context, input NodeConfigPatchInput) (*Node, error) {
	if err := requireRuntimeRunID("patch_config", input.RunID); err != nil {
		return nil, err
	}
	return queryOneWrite(ctx, func() (sqlc.PatchTaskDagNodeConfigIfUnchangedRow, error) {
		return s.q.PatchTaskDagNodeConfigIfUnchanged(ctx, sqlc.PatchTaskDagNodeConfigIfUnchangedParams{
			DagKey:         input.DagKey,
			NodeKey:        input.NodeKey,
			Config:         input.Config,
			PreviousConfig: input.PreviousConfig,
			RunID:          int64Ptr(input.RunID),
		})
	}, "patch_config", "task_dag_node", fromNodePatchConfigRow)
}

// DeleteNode 删除节点。
func (s *store) DeleteNode(ctx context.Context, dagKey, nodeKey string) (int64, error) {
	return queryValueWrite(ctx, func() (int64, error) {
		return s.q.DeleteTaskDagNode(ctx, sqlc.DeleteTaskDagNodeParams{
			DagKey:  dagKey,
			NodeKey: nodeKey,
		})
	}, "delete", "task_dag_node")
}

// UpdateNodeStatus 更新节点状态。
func (s *store) UpdateNodeStatus(ctx context.Context, input NodeStatusUpdate) (*Node, error) {
	if err := requireRuntimeRunID("update_status", input.RunID); err != nil {
		return nil, err
	}
	// R1 dead code 清理：原 2 份 SQL（UpdateTaskDagNodeStatus / UpdateTaskDagNodeStatusFlexible）
	// 逻辑上完全重复，合并为 Flexible 一份。本函数保留为发布层 sentinel（NodeStatusUpdate
	// 与 FlexibleNodeStatusUpdate 输入名字不同），但底层走同一 query。
	return updateNodeStatusWrite(ctx, func() (sqlc.UpdateTaskDagNodeStatusFlexibleRow, error) {
		return s.q.UpdateTaskDagNodeStatusFlexible(ctx, sqlc.UpdateTaskDagNodeStatusFlexibleParams{
			Status:  input.Status,
			Result:  input.Result,
			DagKey:  input.DagKey,
			NodeKey: input.NodeKey,
			RunID:   int64Ptr(input.RunID),
		})
	}, "update_status", fromNodeStatusFlexibleRow)
}

// ListNodes 列出节点。
func (s *store) ListNodes(ctx context.Context, dagKey string) ([]Node, error) {
	return queryMany(func() ([]sqlc.ListTaskDagNodesRow, error) {
		return s.q.ListTaskDagNodes(ctx, sqlc.ListTaskDagNodesParams{DagKey: dagKey})
	}, "list", "task_dag_node", fromNodeListRow)
}

// AssignNode 处理assign节点。
func (s *store) AssignNode(ctx context.Context, input AssignNodeInput) (*Node, error) {
	if err := requireRuntimeRunID("assign", input.RunID); err != nil {
		return nil, err
	}
	return queryOneWrite(ctx, func() (sqlc.AssignTaskDagNodeRow, error) {
		return s.q.AssignTaskDagNode(ctx, sqlc.AssignTaskDagNodeParams{
			AssignedTo: input.AssignedTo,
			DagKey:     input.DagKey,
			NodeKey:    input.NodeKey,
			RunID:      int64Ptr(input.RunID),
		})
	}, "assign", "task_dag_node", fromNodeAssignRow)
}

// ListRunNodes 列出运行记录节点。
func (s *store) ListRunNodes(ctx context.Context, dagKey string, runID int64) ([]Node, error) {
	return queryMany(func() ([]sqlc.ListTaskDagRunNodesRow, error) {
		return s.q.ListTaskDagRunNodes(ctx, sqlc.ListTaskDagRunNodesParams{
			DagKey: dagKey,
			RunID:  int64Ptr(runID),
		})
	}, "list_run", "task_dag_node", fromNodeRunListRow)
}

// LookupNodesBySpawningThread reverses task_dag_nodes.spawning_thread_id back
// to the node rows that spawned the given child thread id. ADR-017 v1.2 §2.2.
//
// Empty result slice (not platformdb.ErrNotFound) means no node currently
// carries this thread id. N>1 results are normal on retry / recovery chains
// (migration 0083 partial index has no UNIQUE clause + F1.5 write entry-point
// is not single-writer); the caller iterates and applies idempotent
// advancement on every row.
// LookupNodesBySpawningThread 按spawning线程处理lookup节点。
func (s *store) LookupNodesBySpawningThread(ctx context.Context, threadID string) ([]Node, error) {
	return queryMany(func() ([]sqlc.LookupNodesBySpawningThreadRow, error) {
		return s.q.LookupNodesBySpawningThread(ctx, sqlc.LookupNodesBySpawningThreadParams{SpawningThreadID: sqlc.TextValuePtr(&threadID)})
	}, "lookup_by_spawning_thread", "task_dag_node", fromNodeLookupBySpawningThreadRow)
}

// ListRunningNodesByAssignee 按assignee列出running节点。
func (s *store) ListRunningNodesByAssignee(ctx context.Context, assignee string) ([]Node, error) {
	return queryMany(func() ([]sqlc.ListRunningTaskDagNodesByAssigneeRow, error) {
		return s.q.ListRunningTaskDagNodesByAssignee(ctx, sqlc.ListRunningTaskDagNodesByAssigneeParams{AssignedTo: assignee})
	}, "list_running_by_assignee", "task_dag_node", fromNodeRunningByAssigneeRow)
}

// GetDAGForUpdate 为更新读取DAG。
func (s *store) GetDAGForUpdate(ctx context.Context, dagKey string) (*DAG, error) {
	return queryOne(func() (sqlc.GetTaskDagForUpdateRow, error) {
		return s.q.GetTaskDagForUpdate(ctx, sqlc.GetTaskDagForUpdateParams{DagKey: dagKey})
	}, "get_for_update", "task_dag", fromDAGGetForUpdateRow)
}

// GetNodesForUpdate 为更新读取节点。
func (s *store) GetNodesForUpdate(ctx context.Context, dagKey string) ([]Node, error) {
	return queryMany(func() ([]sqlc.GetTaskDagNodesForUpdateRow, error) {
		return s.q.GetTaskDagNodesForUpdate(ctx, sqlc.GetTaskDagNodesForUpdateParams{DagKey: dagKey})
	}, "get_for_update", "task_dag_node", fromNodeForUpdateRow)
}

// BindRunningNodeTurn 绑定running节点turn。
func (s *store) BindRunningNodeTurn(ctx context.Context, input BindRunningNodeTurnInput) (*Node, error) {
	if err := requireRuntimeRunID("bind_running_turn", input.RunID); err != nil {
		return nil, err
	}
	var mapped Node
	err := sqlctx.WithImmediateTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, _ sqlc.DBTX) error {
		_, err := bindWakeupTurnTx(ctx, txq, BindWakeupTurnInput{
			TurnID: input.TurnID,
			ID:     input.WakeupID,
		}, true)
		if err != nil {
			return err
		}
		row, err := txq.BindRunningTaskDagNodeTurn(ctx, sqlc.BindRunningTaskDagNodeTurnParams{
			ActiveTurnID:   stringPtr(input.TurnID),
			DagKey:         input.DagKey,
			NodeKey:        input.NodeKey,
			ActiveWakeupID: int64Ptr(input.WakeupID),
			RunID:          int64Ptr(input.RunID),
		})
		if err != nil {
			return wrapTaskDAGError(err, "bind_running_turn", "task_dag_node")
		}
		mapped = fromNodeBindTurnRow(row)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &mapped, nil
}

// TouchRunningNodeEvent 处理touchrunning节点事件。
func (s *store) TouchRunningNodeEvent(ctx context.Context, input TouchRunningNodeEventInput) (*Node, error) {
	if err := requireRuntimeRunID("touch_running_event", input.RunID); err != nil {
		return nil, err
	}
	return queryOneWrite(ctx, func() (sqlc.TouchRunningTaskDagNodeEventRow, error) {
		return s.q.TouchRunningTaskDagNodeEvent(ctx, sqlc.TouchRunningTaskDagNodeEventParams{
			LastEventAt:  timestampValue(input.ObservedAt),
			DagKey:       input.DagKey,
			NodeKey:      input.NodeKey,
			ActiveTurnID: stringPtr(input.TurnID),
			RunID:        int64Ptr(input.RunID),
		})
	}, "touch_running_event", "task_dag_node", fromNodeTouchEventRow)
}

// UpdateRunningNodeStatus 更新running节点状态。
func (s *store) UpdateRunningNodeStatus(ctx context.Context, input RunningNodeStatusUpdate) (*Node, error) {
	if err := requireRuntimeRunID("update_running_status", input.RunID); err != nil {
		return nil, err
	}
	return updateNodeStatusWrite(ctx, func() (sqlc.UpdateRunningTaskDagNodeStatusRow, error) {
		return s.q.UpdateRunningTaskDagNodeStatus(ctx, sqlc.UpdateRunningTaskDagNodeStatusParams{
			Status:         input.Status,
			Result:         input.Result,
			ActiveWakeupID: int64Ptr(input.WakeupID),
			DagKey:         input.DagKey,
			NodeKey:        input.NodeKey,
			RunID:          int64Ptr(input.RunID),
		})
	}, "update_running_status", fromNodeUpdateRunningRow)
}

// UpdateAwaitingVerifyNodeStatus 更新awaitingverify节点状态。
func (s *store) UpdateAwaitingVerifyNodeStatus(ctx context.Context, input AwaitingVerifyNodeStatusUpdate) (*Node, error) {
	if err := requireRuntimeRunID("update_awaiting_verify_status", input.RunID); err != nil {
		return nil, err
	}
	return updateNodeStatusWrite(ctx, func() (sqlc.UpdateAwaitingVerifyTaskDagNodeStatusRow, error) {
		return s.q.UpdateAwaitingVerifyTaskDagNodeStatus(ctx, sqlc.UpdateAwaitingVerifyTaskDagNodeStatusParams{
			Status:  input.Status,
			Result:  input.Result,
			DagKey:  input.DagKey,
			NodeKey: input.NodeKey,
			RunID:   int64Ptr(input.RunID),
		})
	}, "update_awaiting_verify_status", fromNodeUpdateAwaitingVerifyRow)
}

// CompleteNode 完成节点。
func (s *store) CompleteNode(ctx context.Context, input CompleteNodeInput) (*Node, error) {
	if err := requireRuntimeRunID("complete", input.RunID); err != nil {
		return nil, err
	}
	return updateNodeStatusWrite(ctx, func() (sqlc.CompleteTaskDagNodeRow, error) {
		return s.q.CompleteTaskDagNode(ctx, sqlc.CompleteTaskDagNodeParams{
			Status:  input.Status,
			Result:  input.Result,
			DagKey:  input.DagKey,
			NodeKey: input.NodeKey,
			RunID:   int64Ptr(input.RunID),
		})
	}, "complete", fromNodeCompleteRow)
}

// UpdateNodeStatusFlexible 更新节点状态flexible。
func (s *store) UpdateNodeStatusFlexible(ctx context.Context, input FlexibleNodeStatusUpdate) (*Node, error) {
	if err := requireRuntimeRunID("update_status_flexible", input.RunID); err != nil {
		return nil, err
	}
	return updateNodeStatusWrite(ctx, func() (sqlc.UpdateTaskDagNodeStatusFlexibleRow, error) {
		return s.q.UpdateTaskDagNodeStatusFlexible(ctx, sqlc.UpdateTaskDagNodeStatusFlexibleParams{
			Status:  input.Status,
			Result:  input.Result,
			DagKey:  input.DagKey,
			NodeKey: input.NodeKey,
			RunID:   int64Ptr(input.RunID),
		})
	}, "update_status_flexible", fromNodeStatusFlexibleRow)
}

// ClaimNodeOutputMaterialization 领取节点输出物化任务。
func (s *store) ClaimNodeOutputMaterialization(ctx context.Context, input OutputMaterializationClaimInput) (*Node, error) {
	if err := requireRuntimeRunID("claim_output_materialization", input.RunID); err != nil {
		return nil, err
	}
	return updateNodeStatusWrite(ctx, func() (sqlc.ClaimTaskDagNodeOutputMaterializationRow, error) {
		return s.q.ClaimTaskDagNodeOutputMaterialization(ctx, sqlc.ClaimTaskDagNodeOutputMaterializationParams{
			Result:  input.Result,
			DagKey:  input.DagKey,
			NodeKey: input.NodeKey,
			RunID:   int64Ptr(input.RunID),
		})
	}, "claim_output_materialization", fromNodeClaimOutputRow)
}

func int64Ptr(value int64) sqlc.Int8 {
	return sqlc.Int8ValuePtr(&value)
}

func stringPtr(value string) sqlc.Text {
	return sqlc.TextValuePtr(&value)
}

func fromDAGListRow(row sqlc.ListTaskDagsRow) DAG {
	return fromDAGRaw(row.ID, row.DagKey, row.Version, row.Title, row.Description, row.Status, row.CreatedBy, row.Metadata, row.Trigger, row.CronExpr, row.NextRunAt, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt)
}

func wrapTaskDAGError(err error, operation, entity string) error {
	return platformdb.WrapStoreError(err, operation, entity)
}

// intervalValue converts textual interval input into epoch-millisecond deltas expected by SQLite sqlc queries.
func intervalValue(value string) (sqlc.Interval, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, errors.New("interval is required")
	}
	if duration, err := time.ParseDuration(trimmed); err == nil {
		return sqlc.IntervalMillis(duration), nil
	}
	if duration, ok := parseClockInterval(trimmed); ok {
		return sqlc.IntervalMillis(duration), nil
	}
	fields := strings.Fields(strings.ToLower(trimmed))
	if len(fields) != 2 {
		return 0, fmt.Errorf("invalid interval %q", value)
	}
	amount, err := time.ParseDuration(fields[0] + intervalUnitSuffix(fields[1]))
	if err != nil {
		return 0, fmt.Errorf("invalid interval %q: %w", value, err)
	}
	return sqlc.IntervalMillis(amount), nil
}

func parseClockInterval(value string) (time.Duration, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0, false
	}
	hours, ok := parseClockIntervalPart(parts[0], false)
	if !ok {
		return 0, false
	}
	minutes, ok := parseClockIntervalPart(parts[1], true)
	if !ok {
		return 0, false
	}
	seconds, ok := parseClockIntervalPart(parts[2], true)
	if !ok {
		return 0, false
	}
	return time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second, true
}

func parseClockIntervalPart(value string, bounded bool) (int64, bool) {
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, false
	}
	if bounded && parsed >= 60 {
		return 0, false
	}
	return parsed, true
}

func intervalUnitSuffix(unit string) string {
	switch strings.TrimSuffix(unit, "s") {
	case "millisecond", "msec", "ms":
		return "ms"
	case "second", "sec", "s":
		return "s"
	case "minute", "min", "m":
		return "m"
	case "hour", "hr", "h":
		return "h"
	default:
		return unit
	}
}

func timeValue(value int64) time.Time {
	return sqlc.TimeValue(value)
}

func timestampPtr(value *int64) *time.Time {
	return sqlc.TimePtr(value)
}

func timestampValue(value time.Time) *int64 {
	return sqlc.TimeValuePtr(&value)
}
