package taskdag

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlctx"
)

// Phase 3.4 / 3B · CompleteNode 自动 enqueue 下游：节点完成后查 DAG 拓扑找
// ready 下游节点（依赖已满足），对每个生成 wakeup；同事务保证一致性。
//
// idempotency_key 用 `dag/<dagKey>/<nodeKey>/start` 形式保证：同一 downstream
// 节点重复触发只会 INSERT 一行，第二次起 ON CONFLICT 静默跳过；
// CompleteNodeAndScheduleDownstream 把跳过条目从 ScheduledDownstream 中过滤
// 掉，让调用方 len(result) == 真正新增的 wakeup 数。

const (
	// downstreamWakeupKind 标记 task_dag_wakeups.wakeup_kind 为 \"DAG 拓扑下游
	// 自动派发\"。dispatcher 端不依赖该值做分支，仅作为日志/审计区分点。
	downstreamWakeupKind = "node_start"
	// terminalSuccessStatus 是唯一能满足下游 depends_on 的终态值；failed /
	// canceled 走 Phase 3.5 fail-fast 决策，不算「依赖满足」。
	terminalSuccessStatus = "done"
)

func (s *store) CompleteNodeAndScheduleDownstream(ctx context.Context, input CompleteNodeInput) (*CompleteNodeWithDownstreamResult, error) {
	if err := requireRuntimeRunID("complete_and_schedule_downstream", input.RunID); err != nil {
		return nil, err
	}
	var result CompleteNodeWithDownstreamResult
	err := sqlctx.WithImmediateTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, txdb sqlc.DBTX) error {
		txStore := &store{db: txdb, q: txq}
		if err := lockRunForCompletionTx(ctx, txStore, input.DagKey, input.RunID); err != nil {
			return err
		}
		node, completeErr := txStore.CompleteNode(ctx, input)
		if completeErr != nil {
			return completeErr
		}
		result.Node = node
		if node == nil {
			return nil
		}
		// 下游调度仅限 done 终态：failed / cancelled / skipped 走下游不会被
		// dependsOn 满足，不需 enqueue。但 F6.2 终态判定在任何终态后都要尝试。
		// Downstream scheduling stays gated on done — failed/cancelled/skipped
		// terminals do not satisfy dependsOn and need no enqueue.
		if node.Status == terminalSuccessStatus {
			scheduled, promoted, scheduleErr := scheduleDownstreamWakeupsTx(ctx, txStore, node)
			if scheduleErr != nil {
				return scheduleErr
			}
			result.ScheduledDownstream = scheduled
			result.PromotedDownstream = promoted
		}
		// F6.2 同事务内推进 run.status：节点全终态时按优先级
		// (failed > cancelled > succeeded) 写 task_dag_runs.status + finished_at。
		// F6.2: same-tx run finalize — priority failed > cancelled > succeeded.
		finalized, finalizeErr := maybeFinalizeRunTx(ctx, txStore, node.DagKey, runIDValue(node.RunID))
		if finalizeErr != nil {
			return finalizeErr
		}
		result.FinalizedRun = finalized
		return nil
	})
	if err != nil {
		return nil, wrapTaskDAGError(err, "complete_and_schedule_downstream", "task_dag_node")
	}
	return &result, nil
}

func lockRunForCompletionTx(ctx context.Context, txStore *store, dagKey string, runID int64) error {
	if err := requireRuntimeRunID("lock_run_for_completion", runID); err != nil {
		return err
	}
	if _, err := txStore.q.LockTaskDagRunForCompletionForUpdate(ctx, sqlc.LockTaskDagRunForCompletionForUpdateParams{
		DagKey: dagKey,
		ID:     runID,
	}); err != nil {
		return fmt.Errorf("lock run %s/%d for completion: %w", dagKey, runID, err)
	}
	return nil
}

// maybeFinalizeRunTx 调 sqlc 生代的 FinalizeTaskDagRunIfAllNodesTerminal，把
// “节点全终态时按优先级推进 run.status” 一句 SQL 完成。最多返 1 行
// (run_key, status)；返 0 行表示节点未全部终态、或 dag_key 下无 running run。
//
// maybeFinalizeRunTx invokes the F6.2 finalize SQL; it is idempotent because
// the WHERE clause only matches a 'running' run, so re-running it after the
// first successful flip simply returns zero rows.
func maybeFinalizeRunTx(ctx context.Context, txStore *store, dagKey string, runID int64) (*FinalizedRunInfo, error) {
	if err := requireRuntimeRunID("finalize_run", runID); err != nil {
		return nil, err
	}
	rows, err := txStore.q.FinalizeTaskDagRunIfAllNodesTerminal(ctx, sqlc.FinalizeTaskDagRunIfAllNodesTerminalParams{
		DagKey: dagKey,
		RunID:  int64Ptr(runID),
	})
	if err != nil {
		return nil, fmt.Errorf("finalize run for dag %s: %w", dagKey, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	// SQL WHERE dag_key=$1 + status='running' 命中至多 1 行（56 partial unique
	// guarantees a single running run per dag_key）；取首行、多余行是 DB drift 不静默。
	// At most one running run per dag_key (0076 partial unique); take the first.
	first := rows[0]
	return &FinalizedRunInfo{RunKey: first.RunKey, Status: first.Status}, nil
}

// scheduleDownstreamWakeupsTx assumes caller has already completed the
// upstream node row inside txStore's transaction. It re-reads the entire DAG
// node set (cheap; DAGs are tens of nodes max) so it can evaluate every
// downstream candidate's depends_on against the post-completion status map.
//
// F6.3: 同事务内对每个「依赖已满足」的 pending 下游节点做两件事：
//  1. promote pending→ready（PromoteSingleNodePendingToReady SQL）—— 无论
//     assigned_to 是否为空，状态机层面都要推进，以暴露「ready 但未指派」
//     可观测态；下次外部接管 / 人工补 assigned_to 时直接进 dispatcher。
//  2. 仅当 assigned_to 非空时，enqueue wakeup（F6.4 跳过空 assigned_to 路由
//     仍然成立）。
//
// EN: F6.3 + F6.4 dual responsibility — every dependency-satisfied pending
// downstream node is promoted to ready in the same tx, while wakeup enqueue
// stays gated on a non-empty assigned_to per F6.4. The two concerns are
// decoupled: promote = state-machine truth; enqueue = dispatch routing.
func scheduleDownstreamWakeupsTx(ctx context.Context, txStore *store, completed *Node) ([]ScheduledDownstreamWakeup, []PromotedDownstreamNode, error) {
	completedRunID := runIDValue(completed.RunID)
	if err := requireRuntimeRunID("schedule_downstream", completedRunID); err != nil {
		return nil, nil, err
	}
	nodes, listErr := txStore.ListRunNodes(ctx, completed.DagKey, completedRunID)
	if listErr != nil {
		return nil, nil, listErr
	}
	statusByKey := make(map[string]string, len(nodes))
	for i := range nodes {
		statusByKey[nodes[i].NodeKey] = nodes[i].Status
	}
	scheduled := make([]ScheduledDownstreamWakeup, 0)
	promoted := make([]PromotedDownstreamNode, 0)
	for i := range nodes {
		cand := &nodes[i]
		inserted, promotedNode, err := scheduleDownstreamCandidateTx(ctx, txStore, cand, completed, statusByKey)
		if err != nil {
			return nil, nil, err
		}
		if promotedNode != nil {
			promoted = append(promoted, *promotedNode)
		}
		if inserted != nil {
			scheduled = append(scheduled, *inserted)
		}
	}
	return scheduled, promoted, nil
}

func scheduleDownstreamCandidateTx(
	ctx context.Context,
	txStore *store,
	cand *Node,
	completed *Node,
	statusByKey map[string]string,
) (*ScheduledDownstreamWakeup, *PromotedDownstreamNode, error) {
	satisfied, err := dependsSatisfiedForUpstream(cand, completed.NodeKey, statusByKey)
	if err != nil || !satisfied {
		return nil, nil, err
	}
	promoted, err := promoteDownstreamCandidateTx(ctx, txStore, cand, runIDValue(completed.RunID))
	if err != nil || promoted == nil {
		return nil, promoted, err
	}
	inserted, err := tryEnqueueDownstream(ctx, txStore, cand)
	return inserted, promoted, err
}

func promoteDownstreamCandidateTx(ctx context.Context, txStore *store, cand *Node, completedRunID int64) (*PromotedDownstreamNode, error) {
	rowsAffected, err := txStore.q.PromoteSingleNodePendingToReady(ctx, sqlc.PromoteSingleNodePendingToReadyParams{
		DagKey:  cand.DagKey,
		NodeKey: cand.NodeKey,
		RunID:   int64Ptr(completedRunID),
	})
	if err != nil {
		return nil, fmt.Errorf("promote %s/%s pending→ready: %w", cand.DagKey, cand.NodeKey, err)
	}
	if rowsAffected == 0 {
		return nil, nil
	}
	return &PromotedDownstreamNode{DagKey: cand.DagKey, NodeKey: cand.NodeKey, RunID: completedRunID}, nil
}

// dependsSatisfiedForUpstream 判定 cand 是否：
//   - 当前还在 pending（未被并发推进）
//   - depends_on 包含 completedKey（即真的是该上游的下游）
//   - 所有 depends_on 都已 done
func dependsSatisfiedForUpstream(cand *Node, completedKey string, statusByKey map[string]string) (bool, error) {
	if cand.Status != "pending" {
		return false, nil
	}
	deps, depErr := decodeDependsOn(cand.DependsOn)
	if depErr != nil {
		return false, fmt.Errorf("decode depends_on for %s/%s: %w", cand.DagKey, cand.NodeKey, depErr)
	}
	if !dependsOnIncludes(deps, completedKey) {
		return false, nil
	}
	return allDependenciesSatisfied(deps, statusByKey), nil
}

// tryEnqueueDownstream enqueues a downstream wakeup for a candidate node
// whose dependencies have already been confirmed satisfied (and promoted
// pending→ready) by the caller. Returns (nil, nil) when assigned_to is empty
// (F6.4 routing skip) or when the INSERT was deduped by ON CONFLICT.
//
// F6.3 refactor: depends_on / status checks moved up to
// dependsSatisfiedForUpstream so promote + enqueue share a single decision.
func tryEnqueueDownstream(
	ctx context.Context,
	txStore *store,
	cand *Node,
) (*ScheduledDownstreamWakeup, error) {
	agentID := strings.TrimSpace(cand.AssignedTo)
	// F6.4：assigned_to 为空 → 跳过 wakeup enqueue。否则 dispatcher 走
	// LaunchAgent 会因 "agent id is required" 失败，retry 耗尽后把节点判
	// 死成 permanent failed（详见 docs/plans/dag改造实施计划.md follow-up
	// F6.4）。节点已在调用方 promote 到 ready，等待外部 agent / 人工接管
	// 后再进入 running，避免误杀未指派节点。
	//
	// EN: When the candidate has no assigned_to we deliberately skip the
	// wakeup enqueue: otherwise the dispatcher's LaunchAgent rejects the
	// empty agent id, exhausts retries, and the node ends up permanently
	// failed. The caller has already promoted the node to ready, so an
	// external/manual flow can later move it to running.
	if agentID == "" {
		err := fmt.Errorf("assigned_to required for automatic downstream dispatch")
		return nil, appendDispatchBlockedEvent(ctx, txStore, cand, runIDValue(cand.RunID), "downstream", err)
	}
	if err := validateAutomaticDispatchConfig(cand); err != nil {
		return nil, appendDispatchBlockedEvent(ctx, txStore, cand, runIDValue(cand.RunID), "downstream", err)
	}
	candRunID := runIDValue(cand.RunID)
	idempotencyKey := downstreamIdempotencyKey(cand.DagKey, cand.NodeKey, candRunID)
	payload, payloadErr := json.Marshal(DownstreamWakeupPayload{
		AgentID: agentID,
	})
	if payloadErr != nil {
		return nil, fmt.Errorf("marshal payload for %s/%s: %w", cand.DagKey, cand.NodeKey, payloadErr)
	}
	rows, enqErr := txStore.EnqueueWakeup(ctx, EnqueueWakeupInput{
		DagKey:         cand.DagKey,
		NodeKey:        cand.NodeKey,
		RunID:          candRunID,
		WakeupKind:     downstreamWakeupKind,
		TargetAgentID:  agentID,
		PromptPayload:  payload,
		IdempotencyKey: idempotencyKey,
	})
	if enqErr != nil {
		return nil, enqErr
	}
	if rows == 0 {
		// ON CONFLICT (idempotency_key) skipped: a prior upstream
		// completion (or replayed call) already enqueued the same
		// downstream wakeup. Returning nil here keeps the caller's
		// invariant len(scheduled) == new INSERTs this call performed.
		return nil, nil
	}
	return &ScheduledDownstreamWakeup{
		DagKey:         cand.DagKey,
		NodeKey:        cand.NodeKey,
		RunID:          candRunID,
		TargetAgentID:  agentID,
		IdempotencyKey: idempotencyKey,
	}, nil
}

func runIDValue(runID *int64) int64 {
	if runID == nil {
		return 0
	}
	return *runID
}

func decodeDependsOn(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var deps []string
	if err := json.Unmarshal(raw, &deps); err != nil {
		return nil, err
	}
	return deps, nil
}

func dependsOnIncludes(deps []string, nodeKey string) bool {
	for _, d := range deps {
		if strings.TrimSpace(d) == nodeKey {
			return true
		}
	}
	return false
}

func allDependenciesSatisfied(deps []string, statusByKey map[string]string) bool {
	for _, d := range deps {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if statusByKey[d] != terminalSuccessStatus {
			return false
		}
	}
	return true
}

// downstreamIdempotencyKey 返回 `dag/<dagKey>/<nodeKey>/start` 形式的去重键。
// dispatcher 端永远不要依赖这个键做语义分支；它只是 SQL ON CONFLICT 的输入。
func downstreamIdempotencyKey(dagKey, nodeKey string, runID int64) string {
	if runID > 0 {
		return fmt.Sprintf("dag/%s/run/%d/%s/start", dagKey, runID, nodeKey)
	}
	return "dag/" + dagKey + "/" + nodeKey + "/start"
}
