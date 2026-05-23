package taskdag

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

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
	if len(deps) != 0 || agentID == "" {
		return 0, nil
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

func ManualDispatchIdempotencyKey(dagKey, nodeKey string, runID int64, assignedTo string) string {
	return fmt.Sprintf("manual_dispatch:%s:%d:%s:%s", dagKey, runID, nodeKey, strings.TrimSpace(assignedTo))
}
