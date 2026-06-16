package eventcore

import "time"

const (
	EventTypeAgentStateChanged    uint32 = 1000
	EventTypeAgentLaunched        uint32 = 1001
	EventTypeAgentStopped         uint32 = 1002
	EventTypeAgentRecovering      uint32 = 1003
	EventTypeAgentFailed          uint32 = 1004
	EventTypeAgentRuntimeReported uint32 = 1005
	EventTypeAgentWarning         uint32 = 1006
	EventTypeAgentError           uint32 = 1007

	EventTypeTurnStarted       uint32 = 1100
	EventTypeTurnCompleted     uint32 = 1101
	EventTypeTurnInterrupted   uint32 = 1102
	EventTypeTurnStalled       uint32 = 1103
	EventTypeTurnResumed       uint32 = 1104
	EventTypeTurnInputReceived uint32 = 1105
	EventTypeTurnOutputDelta   uint32 = 1106
	EventTypeTurnPlanDelta     uint32 = 1107
	EventTypeTurnPlanUpdated   uint32 = 1108
	EventTypeTurnItemStarted   uint32 = 1109
	EventTypeTurnItemCompleted uint32 = 1110

	EventTypeToolCallBegin         uint32 = 1200
	EventTypeToolCallEnd           uint32 = 1201
	EventTypeToolApprovalRequested uint32 = 1202
	EventTypeToolApprovalResolved  uint32 = 1203
	EventTypeToolDiffUpdated       uint32 = 1204

	EventTypeTaskDagCreated        uint32 = 1300
	EventTypeTaskNodeStatusChanged uint32 = 1301
	EventTypeTaskWakeupDispatched  uint32 = 1302
	EventTypeTaskWakeupCompleted   uint32 = 1303

	EventTypeCronJobRunStateChanged uint32 = 1400

	EventTypeThreadStarted      uint32 = 1350
	EventTypeThreadStopped      uint32 = 1351
	EventTypeThreadMessagesPage uint32 = 1352
	EventTypeThreadCompacted    uint32 = 1353
	EventTypeThreadUpdated      uint32 = 1354
	EventTypeThreadLaunched     uint32 = 1355

	EventTypeUIProjectionUpdated  uint32 = 1500
	EventTypeUITimelineAppended   uint32 = 1501
	EventTypeUITokensUpdated      uint32 = 1502
	EventTypeUISkillsChanged      uint32 = 1503
	EventTypeUIThreadPatch        uint32 = 1504
	EventTypeUIPreferencesChanged uint32 = 1505
	EventTypeUISharedFilesChanged uint32 = 1506
	EventTypeUIMemoryChanged      uint32 = 1507
	EventTypeUIPromptsChanged     uint32 = 1508

	EventTypeProviderRaw uint32 = 1600

	EventTypeWorkspaceRunCreated       uint32 = 1700
	EventTypeWorkspaceRunStatusChanged uint32 = 1701
	EventTypeWorkspaceRunMerged        uint32 = 1702
	EventTypeWorkspaceRunAborted       uint32 = 1703
	EventTypeWorkspaceRunMergeError    uint32 = 1704
)

// EventHeader carries fields shared by all typed events.
type EventHeader struct {
	Timestamp time.Time `json:"timestamp"`
}

// ThreadHeader identifies a thread-scoped event.
type ThreadHeader struct {
	EventHeader
	ThreadID string `json:"thread_id,omitempty"`
}

// AgentHeader identifies an agent-scoped event.
type AgentHeader struct {
	ThreadHeader
	AgentID string `json:"agent_id"`
}

// AgentSessionHeader identifies an event tied to an agent session.
type AgentSessionHeader struct {
	AgentHeader
	SessionID string `json:"session_id,omitempty"`
}

// TurnIDHeader identifies a turn within an existing event scope.
type TurnIDHeader struct {
	TurnID string `json:"turn_id,omitempty"`
}

// TurnHeader identifies a turn-scoped event.
type TurnHeader struct {
	AgentHeader
	TurnIDHeader
}

// ToolCallHeader identifies a tool call within a turn.
type ToolCallHeader struct {
	TurnHeader
	CallID   string `json:"call_id"`
	ToolName string `json:"tool_name"`
}

// ToolApprovalHeader identifies an approval decision for a tool call.
type ToolApprovalHeader struct {
	ToolCallHeader
	ApprovalID string `json:"approval_id,omitempty"`
}

// DAGHeader identifies an event tied to a DAG.
type DAGHeader struct {
	EventHeader
	DagKey string `json:"dag_key,omitempty"`
}

// TaskDAGHeader identifies a DAG-scoped task event.
type TaskDAGHeader struct {
	DAGHeader
}

// TaskNodeHeader identifies a DAG node-scoped task event.
type TaskNodeHeader struct {
	TaskDAGHeader
	NodeKey string `json:"node_key"`
	RunID   int64  `json:"run_id,omitempty"`
	RunKey  string `json:"run_key,omitempty"`
}

// TaskWakeupHeader identifies a DAG wakeup-scoped task event.
type TaskWakeupHeader struct {
	TaskNodeHeader
	WakeupID int64 `json:"wakeup_id"`
}

// UIProjectionHeader identifies a UI projection event.
type UIProjectionHeader struct {
	ThreadHeader
	Projection string `json:"projection"`
}

// UITurnHeader identifies a turn-scoped UI projection event.
type UITurnHeader struct {
	UIProjectionHeader
	TurnIDHeader
}
