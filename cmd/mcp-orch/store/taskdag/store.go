package taskdag

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
)

// ErrDAGDeleteActiveRun 是删除 DAG 时存在活跃 run 的哨兵错误。
var ErrDAGDeleteActiveRun = errors.New("task_dag: delete blocked by active running run")

// store 是 taskdag 包的内部实现，持有 DB 连接和 sqlc 查询集。
type store struct {
	db sqlc.DBTX
	q  *sqlc.Queries

	*dagCommandStore
	*dagOutputStore
	*dagQueryStore
	*wakeupCommandStore
	*wakeupLeaseStore
	*wakeupQueryStore
}

type dagCommandStore struct {
	db sqlc.DBTX
	q  *sqlc.Queries
}

type dagOutputStore struct {
	q *sqlc.Queries
}

type dagQueryStore struct {
	q *sqlc.Queries
}

// NewStore 创建 taskdag 存储实现，并复用同一 sqlc 查询集封装所有窄接口。
func NewStore(db sqlc.DBTX) Store { return newStoreWithQueries(db, sqlc.New(db)) }

func newStoreWithQueries(db sqlc.DBTX, q *sqlc.Queries) *store {
	return &store{
		db:                 db,
		q:                  q,
		dagCommandStore:    &dagCommandStore{db: db, q: q},
		dagOutputStore:     &dagOutputStore{q: q},
		dagQueryStore:      &dagQueryStore{q: q},
		wakeupCommandStore: newWakeupCommandStore(db, q),
		wakeupLeaseStore:   newWakeupLeaseStore(q),
		wakeupQueryStore:   newWakeupQueryStore(q),
	}
}

// requireRuntimeRunID 确保 run_id 非零；零值意味着调用方传入了模板节点操作，应 fail-fast。
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
		return fn(newStoreWithQueries(tx, txq))
	}), "with_tx", "task_dag")
}

// UpsertDAG 创建或更新 DAG 模板行，只影响模板元数据，不触碰已展开的 runtime run。
func (s *dagCommandStore) UpsertDAG(ctx context.Context, dag DAG) (*DAG, error) {
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

// ListDAGs 按状态、关键字和 limit 下推过滤；空过滤由 SQL 返回默认可见模板集合。
func (s *dagQueryStore) ListDAGs(ctx context.Context, filter ListDAGsFilter) ([]DAG, error) {
	return queryMany(func() ([]sqlc.ListTaskDagsRow, error) {
		return s.q.ListTaskDags(ctx, sqlc.ListTaskDagsParams{
			StatusFilter: filter.Status,
			Keyword:      filter.Keyword,
			LimitCount:   int64(filter.Limit),
		})
	}, "list", "task_dag", fromDAGListRow)
}

// GetDAG 按 dag_key 读取 DAG 模板行；未找到返回 platformdb.ErrNotFound。
func (s *store) GetDAG(ctx context.Context, dagKey string) (*DAG, error) {
	return queryOne(func() (sqlc.GetTaskDagRow, error) {
		return s.q.GetTaskDag(ctx, sqlc.GetTaskDagParams{DagKey: dagKey})
	}, "get", "task_dag", fromDAGGetRow)
}

// DeleteDAG 在事务内级联删除 DAG 及其节点、wakeup 和 run 记录；
// 存在 running run 时返回 ErrDAGDeleteActiveRun。
func (s *dagCommandStore) DeleteDAG(ctx context.Context, dagKey string) (int64, error) {
	key := strings.TrimSpace(dagKey)
	if key == "" {
		return 0, errors.New("delete task_dag: dag_key is required")
	}
	var rows int64
	err := sqlctx.WithImmediateTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, txdb sqlc.DBTX) error {
		txStore := newStoreWithQueries(txdb, txq)
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

// UpsertNode 创建或更新 DAG 节点模板；runtime 节点副本只能由 StartRun 展开后再改。
func (s *store) UpsertNode(ctx context.Context, node Node) (*Node, error) {
	return queryOneWrite(ctx, func() (sqlc.UpsertTaskDagNodeRow, error) {
		return s.q.UpsertTaskDagNode(ctx, sqlc.UpsertTaskDagNodeParams{
			DagKey:     node.DagKey,
			NodeKey:    node.NodeKey,
			Title:      node.Title,
			NodeType:   node.NodeType,
			AssignedTo: node.AssignedTo,
			DependsOn:  node.DependsOn,
			Reads:      stringSliceJSON(node.Reads),
			Writes:     stringSliceJSON(node.Writes),
			CommandRef: node.CommandRef,
			Config:     node.Config,
		})
	}, "upsert", "task_dag_node", fromNodeUpsertRow)
}

func stringSliceJSON(values []string) json.RawMessage {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		if candidate := strings.TrimSpace(value); candidate != "" {
			trimmed = append(trimmed, candidate)
		}
	}
	if len(trimmed) == 0 {
		return json.RawMessage("[]")
	}
	raw, _ := json.Marshal(trimmed)
	return raw
}

// PatchNodeConfigIfUnchanged 以 previous_config CAS fence 原子更新 runtime 节点的 config。
// fence miss 时 SQL 返回 0 行，store 层向上传 ErrNotFound，service 层判 OCC 冲突。
func (s *dagCommandStore) PatchNodeConfigIfUnchanged(ctx context.Context, input NodeConfigPatchInput) (*Node, error) {
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

// DeleteNode 删除模板节点并返回受影响行数；runtime run 副本不走这个模板删除入口。
func (s *store) DeleteNode(ctx context.Context, dagKey, nodeKey string) (int64, error) {
	return queryValueWrite(ctx, func() (int64, error) {
		return s.q.DeleteTaskDagNode(ctx, sqlc.DeleteTaskDagNodeParams{
			DagKey:  dagKey,
			NodeKey: nodeKey,
		})
	}, "delete", "task_dag_node")
}

// UpdateNodeStatus 更新 runtime 节点状态，要求 run_id 命中副本，避免误写模板节点。
func (s *dagCommandStore) UpdateNodeStatus(ctx context.Context, input NodeStatusUpdate) (*Node, error) {
	if err := requireRuntimeRunID("update_status", input.RunID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.ExpectedStatus) == "" {
		return nil, errors.New("update_status expected status is required")
	}
	return updateNodeStatusWrite(ctx, func() (sqlc.UpdateTaskDagNodeStatusIfCurrentRow, error) {
		return s.q.UpdateTaskDagNodeStatusIfCurrent(ctx, sqlc.UpdateTaskDagNodeStatusIfCurrentParams{
			Status:         input.Status,
			Result:         input.Result,
			DagKey:         input.DagKey,
			NodeKey:        input.NodeKey,
			RunID:          int64Ptr(input.RunID),
			ExpectedStatus: input.ExpectedStatus,
		})
	}, "update_status", fromNodeStatusIfCurrentRow)
}

// ListNodes 列出 dag_key 下的模板节点，用于编辑/展示 DAG 结构，不包含 runtime 副本状态。
func (s *store) ListNodes(ctx context.Context, dagKey string) ([]Node, error) {
	return queryMany(func() ([]sqlc.ListTaskDagNodesRow, error) {
		return s.q.ListTaskDagNodes(ctx, sqlc.ListTaskDagNodesParams{DagKey: dagKey})
	}, "list", "task_dag_node", fromNodeListRow)
}

// AssignNode 将 runtime 节点的 assigned_to 更新为指定 agent id，要求 run_id 非零。
func (s *dagCommandStore) AssignNode(ctx context.Context, input AssignNodeInput) (*Node, error) {
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

// AssignNodeAndEnqueueWakeup 在同一 SQLite 写事务内完成节点指派和 wakeup 入队。
// enqueue 失败必须回滚 assigned_to，避免 task_dispatch_node 留下不可恢复的半写状态。
func (s *dagCommandStore) AssignNodeAndEnqueueWakeup(ctx context.Context, input AssignNodeAndEnqueueWakeupInput) (*AssignNodeAndEnqueueWakeupResult, error) {
	if err := validateAssignNodeAndEnqueueWakeupInput(input); err != nil {
		return nil, err
	}
	var result AssignNodeAndEnqueueWakeupResult
	err := sqlctx.WithImmediateTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, _ sqlc.DBTX) error {
		row, err := txq.AssignTaskDagNode(ctx, sqlc.AssignTaskDagNodeParams{
			AssignedTo: input.Assign.AssignedTo,
			DagKey:     input.Assign.DagKey,
			NodeKey:    input.Assign.NodeKey,
			RunID:      int64Ptr(input.Assign.RunID),
		})
		if err != nil {
			return wrapTaskDAGError(err, "assign", "task_dag_node")
		}
		node := fromNodeAssignRow(row)
		wakeupID, err := txq.EnqueueTaskDagWakeup(ctx, sqlc.EnqueueTaskDagWakeupParams{
			DagKey:         input.Wakeup.DagKey,
			NodeKey:        input.Wakeup.NodeKey,
			RunID:          int64Ptr(input.Wakeup.RunID),
			WakeupKind:     input.Wakeup.WakeupKind,
			TargetAgentID:  input.Wakeup.TargetAgentID,
			PromptPayload:  input.Wakeup.PromptPayload,
			IdempotencyKey: input.Wakeup.IdempotencyKey,
		})
		if err != nil {
			return wrapTaskDAGError(err, "enqueue", "task_dag_wakeup")
		}
		result.Node = &node
		result.WakeupID = wakeupID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// validateAssignNodeAndEnqueueWakeupInput 校验原子派发写入的目标一致性。
// assignment 和 wakeup 必须指向同一个 runtime 节点，否则事务会写出无法恢复的半派发记录。
func validateAssignNodeAndEnqueueWakeupInput(input AssignNodeAndEnqueueWakeupInput) error {
	if err := requireRuntimeRunID("assign_and_enqueue", input.Assign.RunID); err != nil {
		return err
	}
	if err := requireRuntimeRunID("assign_and_enqueue", input.Wakeup.RunID); err != nil {
		return err
	}
	if input.Assign.DagKey != input.Wakeup.DagKey || input.Assign.NodeKey != input.Wakeup.NodeKey || input.Assign.RunID != input.Wakeup.RunID {
		return fmt.Errorf("assign_and_enqueue: assignment and wakeup target mismatch")
	}
	if strings.TrimSpace(input.Assign.AssignedTo) == "" || strings.TrimSpace(input.Wakeup.TargetAgentID) == "" {
		return fmt.Errorf("assign_and_enqueue: assigned_to and target_agent_id required")
	}
	return nil
}

// MarkDispatchIncompleteIfMissingWakeup 标记历史半写的 pending/ready 节点。
// 只有 assigned_to 已写入且找不到未完成 wakeup 时才推进到 dispatch_incomplete。
func (s *dagCommandStore) MarkDispatchIncompleteIfMissingWakeup(ctx context.Context, input MarkDispatchIncompleteInput) (*MarkDispatchIncompleteResult, error) {
	if err := requireRuntimeRunID("mark_dispatch_incomplete", input.RunID); err != nil {
		return nil, err
	}
	var result MarkDispatchIncompleteResult
	err := sqlctx.WithImmediateTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, txdb sqlc.DBTX) error {
		txStore := newStoreWithQueries(txdb, txq)
		node, found, err := findRuntimeNodeForDispatchMark(ctx, txStore, input)
		if err != nil || !found {
			return err
		}
		result.Node = &node
		if !isAssignedDispatchCandidate(node, input.AssignedTo) {
			return nil
		}
		active, err := txStore.hasPendingOrDispatchingWakeup(ctx, input)
		if err != nil || active {
			result.ActiveWakeup = active
			return err
		}
		marked, err := txStore.UpdateNodeStatus(ctx, NodeStatusUpdate{
			Status:         "dispatch_incomplete",
			ExpectedStatus: node.Status,
			Result:         json.RawMessage(`{"kind":"dispatch_incomplete"}`),
			DagKey:         input.DagKey,
			NodeKey:        input.NodeKey,
			RunID:          input.RunID,
		})
		if err != nil {
			return err
		}
		result.Marked = marked != nil
		result.Node = marked
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func findRuntimeNodeForDispatchMark(ctx context.Context, s *store, input MarkDispatchIncompleteInput) (Node, bool, error) {
	row, err := s.q.GetTaskDagRunNodeForUpdate(ctx, sqlc.GetTaskDagRunNodeForUpdateParams{
		DagKey:  input.DagKey,
		NodeKey: input.NodeKey,
		RunID:   int64Ptr(input.RunID),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Node{}, false, nil
		}
		return Node{}, false, wrapTaskDAGError(err, "get_run_node_for_dispatch_mark", "task_dag_node")
	}
	node := fromNodeRunForUpdateRow(row)
	return node, true, nil
}

func isAssignedDispatchCandidate(node Node, assignedTo string) bool {
	if node.Status != "pending" && node.Status != "ready" {
		return false
	}
	current := strings.TrimSpace(node.AssignedTo)
	if current == "" {
		return false
	}
	expected := strings.TrimSpace(assignedTo)
	return expected == "" || current == expected
}

// hasPendingOrDispatchingWakeup 检查节点是否已经存在未完成的派发唤醒。
// pending、dispatching，以及 sent 但尚未绑定 turn 的 wakeup 都表示派发仍在进行。
func (s *store) hasPendingOrDispatchingWakeup(ctx context.Context, input MarkDispatchIncompleteInput) (bool, error) {
	exists, err := s.q.HasPendingOrDispatchingTaskDagWakeupForNode(ctx, sqlc.HasPendingOrDispatchingTaskDagWakeupForNodeParams{
		RunID:   int64Ptr(input.RunID),
		DagKey:  input.DagKey,
		NodeKey: input.NodeKey,
	})
	if err != nil {
		return false, wrapTaskDAGError(err, "has_pending_or_dispatching", "task_dag_wakeup")
	}
	return exists != 0, nil
}

// ListRunNodes 列出指定 run_id 下的 runtime 节点副本，供调度器按运行态状态图推进。
func (s *store) ListRunNodes(ctx context.Context, dagKey string, runID int64) ([]Node, error) {
	return queryMany(func() ([]sqlc.ListTaskDagRunNodesRow, error) {
		return s.q.ListTaskDagRunNodes(ctx, sqlc.ListTaskDagRunNodesParams{
			DagKey: dagKey,
			RunID:  int64Ptr(runID),
		})
	}, "list_run", "task_dag_node", fromNodeRunListRow)
}

// LookupNodesBySpawningThread 通过 spawning_thread_id 反查产生子线程的 runtime 节点。
// 空结果表示当前没有节点持有该 thread id；多行结果在重试/恢复链路里合法，
// 调用方需要逐行做幂等推进，不能把它当成唯一索引。
func (s *dagQueryStore) LookupNodesBySpawningThread(ctx context.Context, threadID string) ([]Node, error) {
	return queryMany(func() ([]sqlc.LookupNodesBySpawningThreadRow, error) {
		return s.q.LookupNodesBySpawningThread(ctx, sqlc.LookupNodesBySpawningThreadParams{SpawningThreadID: sqlc.TextValuePtr(&threadID)})
	}, "lookup_by_spawning_thread", "task_dag_node", fromNodeLookupBySpawningThreadRow)
}

// ListRunningNodesByAssignee 列出 assignee 当前持有的 running 节点，用于恢复与重复派发防护。
func (s *dagQueryStore) ListRunningNodesByAssignee(ctx context.Context, assignee string) ([]Node, error) {
	return queryMany(func() ([]sqlc.ListRunningTaskDagNodesByAssigneeRow, error) {
		return s.q.ListRunningTaskDagNodesByAssignee(ctx, sqlc.ListRunningTaskDagNodesByAssigneeParams{AssignedTo: assignee})
	}, "list_running_by_assignee", "task_dag_node", fromNodeRunningByAssigneeRow)
}

// GetDAGForUpdate 以 FOR UPDATE 锁读取 DAG 模板行，供事务内串行化使用。
func (s *store) GetDAGForUpdate(ctx context.Context, dagKey string) (*DAG, error) {
	return queryOne(func() (sqlc.GetTaskDagForUpdateRow, error) {
		return s.q.GetTaskDagForUpdate(ctx, sqlc.GetTaskDagForUpdateParams{DagKey: dagKey})
	}, "get_for_update", "task_dag", fromDAGGetForUpdateRow)
}

// GetNodesForUpdate 以 FOR UPDATE 锁批量读取 dag_key 下所有节点，供事务内串行化使用。
func (s *dagQueryStore) GetNodesForUpdate(ctx context.Context, dagKey string) ([]Node, error) {
	return queryMany(func() ([]sqlc.GetTaskDagNodesForUpdateRow, error) {
		return s.q.GetTaskDagNodesForUpdate(ctx, sqlc.GetTaskDagNodesForUpdateParams{DagKey: dagKey})
	}, "get_for_update", "task_dag_node", fromNodeForUpdateRow)
}

// BindRunningNodeTurn 在事务内先绑定 wakeup turn，再把 active_turn_id 写回节点行。
// wakeup fence miss 时返回 binding conflict 错误，阻止节点指向无效 turn。
func (s *dagCommandStore) BindRunningNodeTurn(ctx context.Context, input BindRunningNodeTurnInput) (*Node, error) {
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

// TouchRunningNodeEvent 更新节点的 last_event_at，表示 turn 仍在活跃。
func (s *dagCommandStore) TouchRunningNodeEvent(ctx context.Context, input TouchRunningNodeEventInput) (*Node, error) {
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

// UpdateRunningNodeStatus 更新 running 节点状态，WakeupID fence 防止旧 dispatch 副本覆盖新轮次。
func (s *dagCommandStore) UpdateRunningNodeStatus(ctx context.Context, input RunningNodeStatusUpdate) (*Node, error) {
	if err := requireRuntimeRunID("update_running_status", input.RunID); err != nil {
		return nil, err
	}
	fence, fenced, err := resolveWakeupFence(ctx, input.WakeupID, input.WakeupFence)
	if err != nil {
		return nil, wrapTaskDAGError(err, "update_running_status", "task_dag_node")
	}
	if fenced {
		var mapped Node
		err := sqlctx.WithImmediateTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, _ sqlc.DBTX) error {
			if err := validateWakeupFenceTx(ctx, txq, fence, input.DagKey, input.NodeKey, input.RunID); err != nil {
				return err
			}
			row, updateErr := txq.UpdateRunningTaskDagNodeStatus(ctx, sqlc.UpdateRunningTaskDagNodeStatusParams{
				Status:         input.Status,
				Result:         input.Result,
				ActiveWakeupID: int64Ptr(fence.WakeupID),
				DagKey:         input.DagKey,
				NodeKey:        input.NodeKey,
				RunID:          int64Ptr(input.RunID),
			})
			if updateErr != nil {
				return wrapTaskDAGError(updateErr, "update_running_status", "task_dag_node")
			}
			mapped = fromNodeUpdateRunningRow(row)
			return nil
		})
		if err != nil {
			return nil, wrapTaskDAGError(err, "update_running_status", "task_dag_node")
		}
		return &mapped, nil
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

// CompleteNode 写入 runtime 节点终态和 result，run_id fence 防止模板节点或旧运行被误完成。
func (s *store) CompleteNode(ctx context.Context, input CompleteNodeInput) (*Node, error) {
	if err := requireRuntimeRunID("complete", input.RunID); err != nil {
		return nil, err
	}
	if err := requireWakeupAttemptFence("complete", input.WakeupID, input.WakeupAttempt); err != nil {
		return nil, err
	}
	return updateNodeStatusWrite(ctx, func() (sqlc.CompleteTaskDagNodeRow, error) {
		return s.q.CompleteTaskDagNode(ctx, sqlc.CompleteTaskDagNodeParams{
			Status:        input.Status,
			Result:        input.Result,
			DagKey:        input.DagKey,
			NodeKey:       input.NodeKey,
			RunID:         int64Ptr(input.RunID),
			WakeupID:      input.WakeupID,
			WakeupAttempt: int64(input.WakeupAttempt),
		})
	}, "complete", fromNodeCompleteRow)
}

func requireWakeupAttemptFence(op string, wakeupID int64, wakeupAttempt int32) error {
	switch {
	case wakeupID == 0 && wakeupAttempt == 0:
		return nil
	case wakeupID <= 0:
		return fmt.Errorf("%s task_dag_node: wakeup_id required when wakeup_attempt is set", op)
	case wakeupAttempt <= 0:
		return fmt.Errorf("%s task_dag_node: wakeup_attempt required when wakeup_id is set", op)
	default:
		return nil
	}
}

// ClaimNodeOutputMaterialization 在节点仍可完成时写入 result 作为物化占位。
// 它不再写 legacy awaiting_verify，后续由 CompleteNode 直接把 ready/running 推到 done。
func (s *dagOutputStore) ClaimNodeOutputMaterialization(ctx context.Context, input OutputMaterializationClaimInput) (*Node, error) {
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

// int64Ptr 把 int64 值包装成 sqlc.Int8（SQLite nullable integer）。
func int64Ptr(value int64) sqlc.Int8 {
	return sqlc.Int8ValuePtr(&value)
}

// stringPtr 把 string 值包装成 sqlc.Text（SQLite nullable text）。
func stringPtr(value string) sqlc.Text {
	return sqlc.TextValuePtr(&value)
}

// fromDAGListRow 把 ListTaskDagsRow 投影成 contract DAG。
func fromDAGListRow(row sqlc.ListTaskDagsRow) DAG {
	return fromDAGRaw(row.ID, row.DagKey, row.Version, row.Title, row.Description, row.Status, row.CreatedBy, row.Metadata, row.Trigger, row.CronExpr, row.NextRunAt, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt)
}

// wrapTaskDAGError 把 database/sql 错误包装为 platformdb 统一域错误。
func wrapTaskDAGError(err error, operation, entity string) error {
	return platformdb.WrapStoreError(err, operation, entity)
}

// intervalValue 解析工具层传入的自然语言区间，并转成 SQLite 查询使用的毫秒差值。
// 空值或无法识别的单位会立即报错，避免调度 lease/重试间隔静默落到 0。
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

// parseClockInterval 解析 HH:MM:SS 格式的时钟区间，不满足格式返回 false。
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

// parseClockIntervalPart 解析时钟区间的单个字段，bounded=true 时要求值 < 60。
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

// intervalUnitSuffix 把自然语言单位名映射到 Go time.ParseDuration 接受的后缀。
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

// timeValue 把 SQLite epoch 毫秒整数转为 time.Time。
func timeValue(value int64) time.Time {
	return sqlc.TimeValue(value)
}

// timestampPtr 把可空的 epoch 毫秒整数转为 *time.Time；nil 输入返回 nil。
func timestampPtr(value *int64) *time.Time {
	return sqlc.TimePtr(value)
}

// timestampValue 把 time.Time 转为 SQLite epoch 毫秒的可空指针，供 sqlc 写入列使用。
func timestampValue(value time.Time) *int64 {
	return sqlc.TimeValuePtr(&value)
}
