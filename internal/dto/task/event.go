package task

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"

// TaskNodeStatusChanged 报告 DAG 节点状态变化，携带新旧状态和当前绑定的 turn/wakeup。
type TaskNodeStatusChanged struct {
	shared.TaskNodeHeader
	AssignedTo     string `json:"assigned_to,omitempty"`
	OldStatus      string `json:"old_status,omitempty"`
	NewStatus      string `json:"new_status"`
	ActiveTurnID   string `json:"active_turn_id,omitempty"`
	ActiveWakeupID int64  `json:"active_wakeup_id,omitempty"`
}

// Type 返回事件总线使用的稳定类型编号，保持任务节点状态事件可路由。
func (TaskNodeStatusChanged) Type() uint32 { return shared.EventTypeTaskNodeStatusChanged }
