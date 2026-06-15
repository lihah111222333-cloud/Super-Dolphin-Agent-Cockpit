package taskdag

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ScheduleRootWakeups 只为当前 run 中已经 ready 且 depends_on=[] 的根节点
// 入队 wakeup。assigned_to 为空或自动派发配置缺失时只追加 dispatch_blocked
// 事件，节点保持 ready，等待 task_dispatch_node/人工接管。
func (s *store) ScheduleRootWakeups(ctx context.Context, dagKey string, runID int64) (int64, error) {
	if err := requireRuntimeRunID("schedule_root_wakeups", runID); err != nil {
		return 0, err
	}
	nodes, err := s.ListRunNodes(ctx, dagKey, runID)
	if err != nil {
		return 0, err
	}
	var inserted int64
	for i := range nodes {
		rows, err := s.scheduleRootWakeup(ctx, &nodes[i], runID)
		if err != nil {
			return 0, err
		}
		inserted += rows
	}
	return inserted, nil
}

// PromoteAndScheduleRunRoots promotes root runtime nodes to ready and then
// enqueues wakeups for roots that already have an assignee.
//
// 这是 StartDAG 的根节点启动和停止过程入口：promote 和 enqueue 分离，确保未指派
// 根节点也能从 pending 进入可观察的 ready 态，而不是静默停在 pending。
func PromoteAndScheduleRunRoots(ctx context.Context, store RunStore, dagKey string, runID int64) (int64, int64, error) {
	readyRootNodes, err := store.PromoteRootNodesToReady(ctx, dagKey, runID)
	if err != nil {
		return 0, 0, fmt.Errorf("PromoteRootNodesToReady: %w", err)
	}
	scheduledWakeups, err := store.ScheduleRootWakeups(ctx, dagKey, runID)
	if err != nil {
		return 0, 0, fmt.Errorf("ScheduleRootWakeups: %w", err)
	}
	return readyRootNodes, scheduledWakeups, nil
}

// scheduleRootWakeup 是根节点自动派发的最终闸口。它不负责 promote 状态，
// 只检查当前节点是否 ready/root/有 assignee/可自动 dispatch；任何不满足
// 自动派发的情况都不把节点标失败。
func (s *store) scheduleRootWakeup(ctx context.Context, node *Node, runID int64) (int64, error) {
	if node.RunID == nil || *node.RunID != runID {
		return 0, fmt.Errorf("schedule root wakeup: unexpected run_id for node %s/%s", node.DagKey, node.NodeKey)
	}
	if node.Status != "ready" {
		return 0, nil
	}
	deps, err := decodeDependsOn(node.DependsOn)
	if err != nil {
		return 0, fmt.Errorf("schedule root wakeup: decode depends_on for %s/%s: %w", node.DagKey, node.NodeKey, err)
	}
	agentID := strings.TrimSpace(node.AssignedTo)
	if len(deps) != 0 {
		return 0, nil
	}
	if agentID == "" {
		return 0, appendDispatchBlockedEvent(ctx, s, node, runID, "root", fmt.Errorf("assigned_to required for automatic root dispatch"))
	}
	if err := validateAutomaticDispatchConfig(node); err != nil {
		return 0, appendDispatchBlockedEvent(ctx, s, node, runID, "root", err)
	}
	payload, err := json.Marshal(DownstreamWakeupPayload{AgentID: agentID})
	if err != nil {
		return 0, fmt.Errorf("schedule root wakeup: marshal payload for %s/%s: %w", node.DagKey, node.NodeKey, err)
	}
	return s.EnqueueWakeup(ctx, EnqueueWakeupInput{
		DagKey:         node.DagKey,
		NodeKey:        node.NodeKey,
		RunID:          runID,
		WakeupKind:     downstreamWakeupKind,
		TargetAgentID:  agentID,
		PromptPayload:  payload,
		IdempotencyKey: ManualDispatchIdempotencyKey(node.DagKey, node.NodeKey, runID, agentID),
	})
}

// ManualDispatchIdempotencyKey 生成手动调度 root wakeup 的幂等键。
func ManualDispatchIdempotencyKey(dagKey, nodeKey string, runID int64, assignedTo string) string {
	return fmt.Sprintf("manual_dispatch:%s:%d:%s:%s", dagKey, runID, nodeKey, strings.TrimSpace(assignedTo))
}
