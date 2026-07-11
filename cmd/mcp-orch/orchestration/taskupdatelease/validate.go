// Package taskupdatelease 校验 task_update_node 的调用者是否仍持有 worker lease。
package taskupdatelease

import (
	"context"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// Validate 用 ToolScope 中可信 agent_id 续约当前节点 assignee 的 worker lease。
// 续约 0 行说明调用者不是 lease holder，调用方必须在写节点状态前失败。
func Validate(ctx context.Context, store any, node taskdag.Node, leaseInterval string) error {
	scope, ok := mcpcommon.ToolScopeFromContext(ctx)
	ownerID := ""
	if ok {
		ownerID = strings.TrimSpace(scope.AgentID)
	}
	targetAgentID := strings.TrimSpace(node.AssignedTo)
	if ownerID == "" || targetAgentID == "" {
		return fmt.Errorf("task_update_node worker lease requires trusted agent_id and assigned_to: dag_key=%s node_key=%s", node.DagKey, node.NodeKey)
	}
	leases, ok := store.(taskdag.WorkerLeaseStore)
	if !ok {
		return fmt.Errorf("task_update_node worker lease requires WorkerLeaseStore for dag_key=%s node_key=%s", node.DagKey, node.NodeKey)
	}
	rows, err := leases.RenewWorkerLease(ctx, taskdag.RenewWorkerLeaseInput{TargetAgentID: targetAgentID, OwnerID: ownerID, LeaseInterval: leaseInterval})
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("task_update_node worker lease rejected: dag_key=%s node_key=%s target_agent_id=%s owner_id=%s", node.DagKey, node.NodeKey, targetAgentID, ownerID)
	}
	return nil
}
