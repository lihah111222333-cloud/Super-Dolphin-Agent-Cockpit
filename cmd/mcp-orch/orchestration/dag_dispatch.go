package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// ErrDispatchStoreUnset 表示 service 被构造时没拿到 taskdag.DispatchNodeStore
// （旧测试路径 / standalone 模式）。MCP 工具层把它转成调用方可读的中英双语错误。
//
// ErrDispatchStoreUnset signals the service was constructed without a
// DispatchNodeStore binding. The MCP tools layer maps it to a bilingual error.
var ErrDispatchStoreUnset = errors.New("orchestration: dispatch store is not configured")

// ErrDispatchNodeIneligible 表示节点当前状态不属于 {pending, ready}，
// 不允许走 DispatchNode 路径（避免误覆盖 running / done / failed 等终态）。
//
// ErrDispatchNodeIneligible signals the node is not in {pending, ready} so the
// dispatch operation refuses to proceed (prevents stomping live runtime state).
var ErrDispatchNodeIneligible = errors.New("orchestration: node is not in pending/ready state, cannot dispatch")

// dispatchNodeWakeupKind 是 task_dispatch_node 入队的 wakeup_kind 值。
// 与 store_complete_downstream.go 的 downstreamWakeupKind 区分：手工显式
// dispatch 用 "manual_dispatch" 让运维 + 日志能一眼分流，避免与依赖完成自动
// enqueue 混淆。
//
// dispatchNodeWakeupKind tags wakeups enqueued by task_dispatch_node so log /
// metric consumers can distinguish manual-dispatch from auto-enqueue.
const dispatchNodeWakeupKind = "manual_dispatch"

// DispatchNode 实现 contract.OrchestrationService.DispatchNode：
//  1. trim + 必填校验入参；
//  2. 用 DispatchNodeStore.ListNodes 拿当前节点状态；
//  3. status 必须 ∈ {pending, ready}；否则返 ErrDispatchNodeIneligible；
//  4. UpsertNode 写入 assigned_to（其它列原样保留）；
//  5. EnqueueWakeup 入队 manual_dispatch wakeup；ON CONFLICT 时 Enqueued=false 也算成功
//     （幂等重放）。
//
// 设计取舍：
//   - 不在事务里跑 list + upsert + enqueue：production *store 默认走单语句路径，
//     ApplyOps / Complete 等真正需要 OCC 的写入才用 WithTx。本工具语义是
//     「外部决策 + 单节点显式推进」，并发冲突由 EnqueueWakeup 的 idempotency_key
//     ON CONFLICT 兜底。
//   - 不允许覆盖已存在的 assigned_to：若节点已有 assignee 且与本次入参不一致，
//     当前实现仍允许覆盖（用户场景：「换 agent 重试」）；要禁用得在上层
//     业务层加策略。
//
// DispatchNode is the ADR-004 explicit-resume entrypoint. Single SQL-call
// granularity is intentional; idempotency on the EnqueueWakeup ON CONFLICT
// covers concurrent dispatch races.
func (s *service) DispatchNode(ctx context.Context, req contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error) {
	if s == nil || s.dispatchStore == nil {
		return contract.DispatchNodeResponse{}, ErrDispatchStoreUnset
	}
	dagKey := strings.TrimSpace(req.DagKey)
	nodeKey := strings.TrimSpace(req.NodeKey)
	assignedTo := strings.TrimSpace(req.AssignedTo)
	if dagKey == "" || nodeKey == "" || assignedTo == "" {
		return contract.DispatchNodeResponse{}, fmt.Errorf("orchestration: DispatchNode: dag_key/node_key/assigned_to required (got %q/%q/%q)", dagKey, nodeKey, assignedTo)
	}

	// 1. 找节点 — 走 ListNodes（窄接口里目前没有单读，沿用 dag.go::validateNodeTransition
	//    同款做法）。生产场景 DAG 节点数量 << 100，扫一遍开销可接受。
	nodes, err := s.dispatchStore.ListNodes(ctx, dagKey)
	if err != nil {
		return contract.DispatchNodeResponse{}, fmt.Errorf("orchestration: DispatchNode list nodes %s: %w", dagKey, err)
	}
	var target *taskdag.Node
	for i := range nodes {
		if nodes[i].NodeKey == nodeKey {
			target = &nodes[i]
			break
		}
	}
	if target == nil {
		return contract.DispatchNodeResponse{}, fmt.Errorf("orchestration: DispatchNode: node %s/%s not found", dagKey, nodeKey)
	}

	// 2. 状态闸：只放 pending / ready 通过。其它状态直接拒绝。
	switch target.Status {
	case "pending", "ready":
		// fall through
	default:
		return contract.DispatchNodeResponse{}, fmt.Errorf("%w (current_status=%q)", ErrDispatchNodeIneligible, target.Status)
	}

	// 3. 写 assigned_to。UpsertNode 是「按 (dag_key, node_key) upsert」，
	//    其它列从 target 原样回写，避免覆盖 config / depends_on / command_ref。
	updated := *target
	updated.AssignedTo = assignedTo
	upserted, err := s.dispatchStore.UpsertNode(ctx, updated)
	if err != nil {
		return contract.DispatchNodeResponse{}, fmt.Errorf("orchestration: DispatchNode upsert %s/%s: %w", dagKey, nodeKey, err)
	}
	if upserted != nil {
		// upserted is the post-write row.
		target = upserted
	}

	// 4. 入队 manual_dispatch wakeup。idempotency_key 用 "manual_dispatch:<dag>:<node>:<assignee>"
	//    让同 assignee 多次 dispatch 被 ON CONFLICT 去重；换 assignee 重试会得到新 row。
	idempotencyKey := fmt.Sprintf("%s:%s:%s:%s", dispatchNodeWakeupKind, dagKey, nodeKey, assignedTo)
	payload, err := json.Marshal(taskdag.DownstreamWakeupPayload{
		AgentID: assignedTo,
	})
	if err != nil {
		return contract.DispatchNodeResponse{}, fmt.Errorf("orchestration: DispatchNode marshal payload: %w", err)
	}
	wakeupID, err := s.dispatchStore.EnqueueWakeup(ctx, taskdag.EnqueueWakeupInput{
		DagKey:         dagKey,
		NodeKey:        nodeKey,
		WakeupKind:     dispatchNodeWakeupKind,
		TargetAgentID:  assignedTo,
		PromptPayload:  payload,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return contract.DispatchNodeResponse{}, fmt.Errorf("orchestration: DispatchNode enqueue %s/%s: %w", dagKey, nodeKey, err)
	}

	resp := contract.DispatchNodeResponse{
		WakeupID: wakeupID,
		Enqueued: wakeupID > 0,
	}
	if target != nil {
		resp.Node = dagNodeDTO(*target)
	}
	return resp, nil
}
