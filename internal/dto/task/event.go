package task

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

// TaskDagCreated reports a DAG entering the system.
type TaskDagCreated struct {
	shared.TaskDAGHeader
	Title     string `json:"title,omitempty"`
	Status    string `json:"status,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
}

// TaskNodeStatusChanged reports a DAG node status transition.
type TaskNodeStatusChanged struct {
	shared.TaskNodeHeader
	AssignedTo     string `json:"assigned_to,omitempty"`
	OldStatus      string `json:"old_status,omitempty"`
	NewStatus      string `json:"new_status"`
	ActiveTurnID   string `json:"active_turn_id,omitempty"`
	ActiveWakeupID int64  `json:"active_wakeup_id,omitempty"`
}

// TaskWakeupDispatched reports a wakeup being sent to a target agent.
type TaskWakeupDispatched struct {
	shared.TaskWakeupHeader
	WakeupKind    string `json:"wakeup_kind,omitempty"`
	TargetAgentID string `json:"target_agent_id"`
}

// TaskWakeupCompleted reports a wakeup finishing its dispatch lifecycle.
type TaskWakeupCompleted struct {
	shared.TaskWakeupHeader
	TargetAgentID string `json:"target_agent_id"`
	Status        string `json:"status"`
	BoundTurnID   string `json:"bound_turn_id,omitempty"`
}

// Type 返回事件分发用的类型编号。
func (TaskDagCreated) Type() uint32 { return shared.EventTypeTaskDagCreated }

// Type 返回事件分发用的类型编号。
func (TaskNodeStatusChanged) Type() uint32 { return shared.EventTypeTaskNodeStatusChanged }

// Type 返回事件分发用的类型编号。
func (TaskWakeupDispatched) Type() uint32 { return shared.EventTypeTaskWakeupDispatched }

// Type 返回事件分发用的类型编号。
func (TaskWakeupCompleted) Type() uint32 { return shared.EventTypeTaskWakeupCompleted }
