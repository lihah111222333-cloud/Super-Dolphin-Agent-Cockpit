package shared

import "time"

const (
	EventTypeAgentStateChanged uint32 = 1000
	EventTypeAgentLaunched     uint32 = 1001
	EventTypeAgentStopped      uint32 = 1002
	EventTypeAgentRecovering   uint32 = 1003
	EventTypeAgentFailed       uint32 = 1004

	EventTypeTurnStarted       uint32 = 1100
	EventTypeTurnCompleted     uint32 = 1101
	EventTypeTurnInterrupted   uint32 = 1102
	EventTypeTurnStalled       uint32 = 1103
	EventTypeTurnResumed       uint32 = 1104
	EventTypeTurnInputReceived uint32 = 1105
	EventTypeTurnOutputDelta   uint32 = 1106

	EventTypeToolCallBegin         uint32 = 1200
	EventTypeToolCallEnd           uint32 = 1201
	EventTypeToolApprovalRequested uint32 = 1202
	EventTypeToolApprovalResolved  uint32 = 1203

	EventTypeTaskDagCreated        uint32 = 1300
	EventTypeTaskNodeStatusChanged uint32 = 1301
	EventTypeTaskWakeupDispatched  uint32 = 1302
	EventTypeTaskWakeupCompleted   uint32 = 1303

	EventTypeWorkspaceRunCreated       uint32 = 1400
	EventTypeWorkspaceRunStatusChanged uint32 = 1401
	EventTypeWorkspaceRunMerged        uint32 = 1402

	EventTypeUIProjectionUpdated uint32 = 1500
	EventTypeUITimelineAppended  uint32 = 1501
	EventTypeUITokensUpdated     uint32 = 1502
)

// EventHeader carries fields shared by all typed events.
type EventHeader struct {
	Timestamp time.Time `json:"timestamp"`
}

// AgentHeader identifies an agent-scoped event.
type AgentHeader struct {
	EventHeader
	AgentID  string `json:"agentId"`
	ThreadID string `json:"threadId"`
}

// AgentSessionHeader identifies an event tied to an agent session.
type AgentSessionHeader struct {
	AgentHeader
	SessionID string `json:"sessionId,omitempty"`
}

// TurnHeader identifies a turn-scoped event.
type TurnHeader struct {
	AgentHeader
	TurnID string `json:"turnId"`
}

// ToolCallHeader identifies a tool call within a turn.
type ToolCallHeader struct {
	TurnHeader
	CallID   string `json:"callId"`
	ToolName string `json:"toolName"`
}

// ToolApprovalHeader identifies an approval decision for a tool call.
type ToolApprovalHeader struct {
	ToolCallHeader
	ApprovalID string `json:"approvalId,omitempty"`
}

// TaskDAGHeader identifies a DAG-scoped task event.
type TaskDAGHeader struct {
	EventHeader
	DagKey string `json:"dagKey"`
}

// TaskNodeHeader identifies a DAG node-scoped task event.
type TaskNodeHeader struct {
	TaskDAGHeader
	NodeKey string `json:"nodeKey"`
}

// TaskWakeupHeader identifies a DAG wakeup-scoped task event.
type TaskWakeupHeader struct {
	TaskNodeHeader
	WakeupID int64 `json:"wakeupId"`
}

// WorkspaceRunHeader identifies a workspace run event.
type WorkspaceRunHeader struct {
	EventHeader
	RunKey string `json:"runKey"`
	DagKey string `json:"dagKey,omitempty"`
}

// UIProjectionHeader identifies a UI projection event.
type UIProjectionHeader struct {
	EventHeader
	Projection string `json:"projection"`
	ThreadID   string `json:"threadId,omitempty"`
}

// UITurnHeader identifies a turn-scoped UI projection event.
type UITurnHeader struct {
	UIProjectionHeader
	TurnID string `json:"turnId,omitempty"`
}
