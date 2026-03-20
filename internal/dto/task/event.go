package task

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

// TaskDagCreated reports a DAG entering the system.
type TaskDagCreated struct {
	shared.TaskDAGHeader
	Title     string `json:"title,omitempty"`
	Status    string `json:"status,omitempty"`
	CreatedBy string `json:"createdBy,omitempty"`
}

// TaskNodeStatusChanged reports a DAG node status transition.
type TaskNodeStatusChanged struct {
	shared.TaskNodeHeader
	AssignedTo     string `json:"assignedTo,omitempty"`
	OldStatus      string `json:"oldStatus,omitempty"`
	NewStatus      string `json:"newStatus"`
	ActiveTurnID   string `json:"activeTurnId,omitempty"`
	ActiveWakeupID int64  `json:"activeWakeupId,omitempty"`
}

// TaskWakeupDispatched reports a wakeup being sent to a target agent.
type TaskWakeupDispatched struct {
	shared.TaskWakeupHeader
	WakeupKind    string `json:"wakeupKind,omitempty"`
	TargetAgentID string `json:"targetAgentId"`
}

// TaskWakeupCompleted reports a wakeup finishing its dispatch lifecycle.
type TaskWakeupCompleted struct {
	shared.TaskWakeupHeader
	TargetAgentID string `json:"targetAgentId"`
	Status        string `json:"status"`
	BoundTurnID   string `json:"boundTurnId,omitempty"`
}

func (TaskDagCreated) Type() uint32        { return shared.EventTypeTaskDagCreated }
func (TaskNodeStatusChanged) Type() uint32 { return shared.EventTypeTaskNodeStatusChanged }
func (TaskWakeupDispatched) Type() uint32  { return shared.EventTypeTaskWakeupDispatched }
func (TaskWakeupCompleted) Type() uint32   { return shared.EventTypeTaskWakeupCompleted }
