package task

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

// TaskDagCreated 报告 DAG 进入任务系统，供编排视图建立初始节点图。
type TaskDagCreated struct {
	shared.TaskDAGHeader
	Title     string `json:"title,omitempty"`
	Status    string `json:"status,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
}

// TaskNodeStatusChanged 报告 DAG 节点状态变化，携带新旧状态和当前绑定的 turn/wakeup。
type TaskNodeStatusChanged struct {
	shared.TaskNodeHeader
	AssignedTo     string `json:"assigned_to,omitempty"`
	OldStatus      string `json:"old_status,omitempty"`
	NewStatus      string `json:"new_status"`
	ActiveTurnID   string `json:"active_turn_id,omitempty"`
	ActiveWakeupID int64  `json:"active_wakeup_id,omitempty"`
}

// TaskWakeupDispatched 报告 wakeup 已向目标 agent 发起派发。
type TaskWakeupDispatched struct {
	shared.TaskWakeupHeader
	WakeupKind    string `json:"wakeup_kind,omitempty"`
	TargetAgentID string `json:"target_agent_id"`
}

// TaskWakeupCompleted 报告 wakeup 派发生命周期结束，结果状态由 Status 承载。
type TaskWakeupCompleted struct {
	shared.TaskWakeupHeader
	TargetAgentID string `json:"target_agent_id"`
	Status        string `json:"status"`
	BoundTurnID   string `json:"bound_turn_id,omitempty"`
}

// Type 返回事件总线使用的稳定类型编号，保持任务 DAG producer/consumer 线协议一致。
func (TaskDagCreated) Type() uint32 { return shared.EventTypeTaskDagCreated }

// Type 返回事件总线使用的稳定类型编号，保持任务节点状态事件可路由。
func (TaskNodeStatusChanged) Type() uint32 { return shared.EventTypeTaskNodeStatusChanged }

// Type 返回事件总线使用的稳定类型编号，保持 wakeup 派发事件可路由。
func (TaskWakeupDispatched) Type() uint32 { return shared.EventTypeTaskWakeupDispatched }

// Type 返回事件总线使用的稳定类型编号，保持 wakeup 完成事件可路由。
func (TaskWakeupCompleted) Type() uint32 { return shared.EventTypeTaskWakeupCompleted }
