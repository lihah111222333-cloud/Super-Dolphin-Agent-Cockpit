package taskdag

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
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
	var result CompleteNodeWithDownstreamResult
	err := sqlc.WithTxOrReuse(ctx, s.q, func(txq *sqlc.Queries) error {
		txStore := &store{q: txq}
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
			scheduled, scheduleErr := scheduleDownstreamWakeupsTx(ctx, txStore, node)
			if scheduleErr != nil {
				return scheduleErr
			}
			result.ScheduledDownstream = scheduled
		}
		// F6.2 同事务内推进 run.status：节点全终态时按优先级
		// (failed > cancelled > succeeded) 写 task_dag_runs.status + finished_at。
		// F6.2: same-tx run finalize — priority failed > cancelled > succeeded.
		finalized, finalizeErr := maybeFinalizeRunTx(ctx, txStore, node.DagKey)
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

// maybeFinalizeRunTx 调 sqlc 生代的 FinalizeTaskDagRunIfAllNodesTerminal，把
// “节点全终态时按优先级推进 run.status” 一句 SQL 完成。最多返 1 行
// (run_key, status)；返 0 行表示节点未全部终态、或 dag_key 下无 running run。
//
// maybeFinalizeRunTx invokes the F6.2 finalize SQL; it is idempotent because
// the WHERE clause only matches a 'running' run, so re-running it after the
// first successful flip simply returns zero rows.
func maybeFinalizeRunTx(ctx context.Context, txStore *store, dagKey string) (*FinalizedRunInfo, error) {
	rows, err := txStore.q.FinalizeTaskDagRunIfAllNodesTerminal(ctx, dagKey)
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
func scheduleDownstreamWakeupsTx(ctx context.Context, txStore *store, completed *Node) ([]ScheduledDownstreamWakeup, error) {
	nodes, listErr := txStore.ListNodes(ctx, completed.DagKey)
	if listErr != nil {
		return nil, listErr
	}
	statusByKey := make(map[string]string, len(nodes))
	for i := range nodes {
		statusByKey[nodes[i].NodeKey] = nodes[i].Status
	}
	upstreamRef := DownstreamUpstreamRef{
		NodeKey: completed.NodeKey,
		Path:    fmt.Sprintf("dag/%s/%s/output.json", completed.DagKey, completed.NodeKey),
	}
	scheduled := make([]ScheduledDownstreamWakeup, 0)
	for i := range nodes {
		inserted, err := tryEnqueueDownstream(ctx, txStore, &nodes[i], completed.NodeKey, statusByKey, upstreamRef)
		if err != nil {
			return nil, err
		}
		if inserted != nil {
			scheduled = append(scheduled, *inserted)
		}
	}
	return scheduled, nil
}

// tryEnqueueDownstream evaluates a single candidate node and, when ready,
// inserts a downstream wakeup. Returns (nil, nil) when the candidate is not
// ready or when the INSERT was deduped by ON CONFLICT.
func tryEnqueueDownstream(
	ctx context.Context,
	txStore *store,
	cand *Node,
	completedKey string,
	statusByKey map[string]string,
	upstreamRef DownstreamUpstreamRef,
) (*ScheduledDownstreamWakeup, error) {
	if cand.Status != "pending" {
		return nil, nil
	}
	deps, depErr := decodeDependsOn(cand.DependsOn)
	if depErr != nil {
		return nil, fmt.Errorf("decode depends_on for %s/%s: %w", cand.DagKey, cand.NodeKey, depErr)
	}
	if !dependsOnIncludes(deps, completedKey) || !allDependenciesSatisfied(deps, statusByKey) {
		return nil, nil
	}
	agentID := strings.TrimSpace(cand.AssignedTo)
	// F6.4：assigned_to 为空 → 跳过 wakeup enqueue。否则 dispatcher 走
	// LaunchAgent 会因 "agent id is required" 失败，retry 耗尽后把节点判
	// 死成 permanent failed（详见 docs/plans/dag改造实施计划.md follow-up
	// F6.4）。节点状态保持 pending（依赖已满足 == ready 语义），等外部
	// agent / 人工接管再 promote 到 running，避免误杀未指派节点。
	//
	// EN: When the candidate has no assigned_to we deliberately skip the
	// wakeup enqueue: otherwise the dispatcher's LaunchAgent rejects the
	// empty agent id, exhausts retries, and the node ends up permanently
	// failed. The node stays in pending (= ready semantics) so an
	// external/manual flow can later move it to running.
	if agentID == "" {
		return nil, nil
	}
	idempotencyKey := downstreamIdempotencyKey(cand.DagKey, cand.NodeKey)
	payload, payloadErr := json.Marshal(DownstreamWakeupPayload{
		AgentID:         agentID,
		UpstreamOutputs: []DownstreamUpstreamRef{upstreamRef},
	})
	if payloadErr != nil {
		return nil, fmt.Errorf("marshal payload for %s/%s: %w", cand.DagKey, cand.NodeKey, payloadErr)
	}
	rows, enqErr := txStore.EnqueueWakeup(ctx, EnqueueWakeupInput{
		DagKey:         cand.DagKey,
		NodeKey:        cand.NodeKey,
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
		TargetAgentID:  agentID,
		IdempotencyKey: idempotencyKey,
	}, nil
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
func downstreamIdempotencyKey(dagKey, nodeKey string) string {
	return "dag/" + dagKey + "/" + nodeKey + "/start"
}
