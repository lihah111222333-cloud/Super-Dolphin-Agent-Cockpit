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
//  2. 用 DispatchNodeStore.ListRunNodes 拿当前 run 的节点状态；
//  3. status 必须 ∈ {pending, ready}；否则返 ErrDispatchNodeIneligible；
//  4. AssignNode 写入 runtime node assigned_to（其它列原样保留）；
//  5. EnqueueWakeup 入队带 run_id 的 manual_dispatch wakeup；ON CONFLICT 时 Enqueued=false 也算成功
//     （幂等重放）。
//
// 设计取舍：
//   - 不在事务里跑 list + assign + enqueue：production *store 默认走单语句路径，
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
	dagKey, nodeKey, assignedTo, runID, err := normalizeDispatchInputs(s, req)
	if err != nil {
		return contract.DispatchNodeResponse{}, err
	}
	target, err := s.findDispatchTarget(ctx, dagKey, nodeKey, runID)
	if err != nil {
		return contract.DispatchNodeResponse{}, err
	}
	if err := ensureDispatchEligible(target); err != nil {
		return contract.DispatchNodeResponse{}, err
	}
	assigned, err := s.assignAndPersist(ctx, target, assignedTo, runID)
	if err != nil {
		return contract.DispatchNodeResponse{}, err
	}
	wakeupID, err := s.enqueueManualDispatchWakeup(ctx, dagKey, nodeKey, runID, assignedTo)
	if err != nil {
		return contract.DispatchNodeResponse{}, err
	}
	resp := contract.DispatchNodeResponse{
		WakeupID: wakeupID,
		Enqueued: wakeupID > 0,
	}
	if assigned != nil {
		resp.Node = dagNodeDTO(*assigned)
	}
	return resp, nil
}

// normalizeDispatchInputs trim 三个必填字段并检查 service / dispatchStore 到位。
// 拆出独立函数是为了压住 DispatchNode 主高的 CC。
func normalizeDispatchInputs(s *service, req contract.DispatchNodeRequest) (string, string, string, int64, error) {
	if s == nil || s.dispatchStore == nil {
		return "", "", "", 0, ErrDispatchStoreUnset
	}
	dagKey := strings.TrimSpace(req.DagKey)
	nodeKey := strings.TrimSpace(req.NodeKey)
	assignedTo := strings.TrimSpace(req.AssignedTo)
	if dagKey == "" || nodeKey == "" || assignedTo == "" {
		return "", "", "", 0, fmt.Errorf("orchestration: DispatchNode: dag_key/node_key/assigned_to required (got %q/%q/%q)", dagKey, nodeKey, assignedTo)
	}
	if req.RunID <= 0 {
		return "", "", "", 0, fmt.Errorf("orchestration: DispatchNode: run_id required for runtime node dispatch (got %d)", req.RunID)
	}
	return dagKey, nodeKey, assignedTo, req.RunID, nil
}

// findDispatchTarget 走 dispatchStore.ListRunNodes 拿到当前 run 的目标节点。
func (s *service) findDispatchTarget(ctx context.Context, dagKey, nodeKey string, runID int64) (*taskdag.Node, error) {
	nodes, err := s.dispatchStore.ListRunNodes(ctx, dagKey, runID)
	if err != nil {
		return nil, fmt.Errorf("orchestration: DispatchNode list run nodes %s run_id=%d: %w", dagKey, runID, err)
	}
	for i := range nodes {
		if nodes[i].NodeKey == nodeKey {
			return &nodes[i], nil
		}
	}
	return nil, fmt.Errorf("orchestration: DispatchNode: node %s/%s not found", dagKey, nodeKey)
}

// ensureDispatchEligible 状态闸：仅放 pending / ready 两状态过。
func ensureDispatchEligible(target *taskdag.Node) error {
	switch target.Status {
	case "pending", "ready":
		return nil
	default:
		return fmt.Errorf("%w (current_status=%q)", ErrDispatchNodeIneligible, target.Status)
	}
}

// assignAndPersist 把 assigned_to 写到 runtime node。其他列原样保留。
func (s *service) assignAndPersist(ctx context.Context, target *taskdag.Node, assignedTo string, runID int64) (*taskdag.Node, error) {
	assigned, err := s.dispatchStore.AssignNode(ctx, taskdag.AssignNodeInput{
		DagKey:     target.DagKey,
		NodeKey:    target.NodeKey,
		RunID:      runID,
		AssignedTo: assignedTo,
	})
	if err != nil {
		return nil, fmt.Errorf("orchestration: DispatchNode assign %s/%s run_id=%d: %w", target.DagKey, target.NodeKey, runID, err)
	}
	return assigned, nil
}

// enqueueManualDispatchWakeup 构建 idempotency_key 并入队 manual_dispatch wakeup。
// 同 assignee 多次 dispatch 被 ON CONFLICT 去重；换 assignee 重试得到新 row。
func (s *service) enqueueManualDispatchWakeup(ctx context.Context, dagKey, nodeKey string, runID int64, assignedTo string) (int64, error) {
	payload, err := json.Marshal(taskdag.DownstreamWakeupPayload{AgentID: assignedTo})
	if err != nil {
		return 0, fmt.Errorf("orchestration: DispatchNode marshal payload: %w", err)
	}
	wakeupID, err := s.dispatchStore.EnqueueWakeup(ctx, taskdag.EnqueueWakeupInput{
		DagKey:         dagKey,
		NodeKey:        nodeKey,
		RunID:          runID,
		WakeupKind:     dispatchNodeWakeupKind,
		TargetAgentID:  assignedTo,
		PromptPayload:  payload,
		IdempotencyKey: taskdag.ManualDispatchIdempotencyKey(dagKey, nodeKey, runID, assignedTo),
	})
	if err != nil {
		return 0, fmt.Errorf("orchestration: DispatchNode enqueue %s/%s: %w", dagKey, nodeKey, err)
	}
	return wakeupID, nil
}
