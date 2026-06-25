package shared

import "time"

// EventType* 是事件总线上所有事件类型的数字编号，按功能域分段。

// Agent 生命周期事件（1000-1099）。
const (
	EventTypeAgentStateChanged    uint32 = 1000
	EventTypeAgentLaunched        uint32 = 1001
	EventTypeAgentStopped         uint32 = 1002
	EventTypeAgentRecovering      uint32 = 1003
	EventTypeAgentFailed          uint32 = 1004
	EventTypeAgentRuntimeReported uint32 = 1005
	EventTypeAgentWarning         uint32 = 1006
	EventTypeAgentError           uint32 = 1007
)

// Turn 生命周期事件（1100-1199）。
const (
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
)

// 工具调用与审批事件（1200-1299）。
const (
	EventTypeToolCallBegin         uint32 = 1200
	EventTypeToolCallEnd           uint32 = 1201
	EventTypeToolApprovalRequested uint32 = 1202
	EventTypeToolApprovalResolved  uint32 = 1203
	EventTypeToolDiffUpdated       uint32 = 1204
)

// DAG 任务事件（1300-1349）。
const (
	EventTypeTaskDagCreated        uint32 = 1300
	EventTypeTaskNodeStatusChanged uint32 = 1301
	EventTypeTaskWakeupDispatched  uint32 = 1302
	EventTypeTaskWakeupCompleted   uint32 = 1303
)

// Thread 生命周期事件（1350-1399）。
const (
	EventTypeThreadStarted      uint32 = 1350
	EventTypeThreadStopped      uint32 = 1351
	EventTypeThreadMessagesPage uint32 = 1352
	EventTypeThreadCompacted    uint32 = 1353
	EventTypeThreadUpdated      uint32 = 1354
	EventTypeThreadLaunched     uint32 = 1355
)

// Cron 任务事件（1400-1499）。
const (
	EventTypeCronJobRunStateChanged uint32 = 1400
)

// UI 投影事件（1500-1599）。
const (
	EventTypeUIProjectionUpdated  uint32 = 1500
	EventTypeUITimelineAppended   uint32 = 1501
	EventTypeUITokensUpdated      uint32 = 1502
	EventTypeUISkillsChanged      uint32 = 1503
	EventTypeUIThreadPatch        uint32 = 1504
	EventTypeUIPreferencesChanged uint32 = 1505
	EventTypeUISharedFilesChanged uint32 = 1506
	EventTypeUIMemoryChanged      uint32 = 1507
	EventTypeUIPromptsChanged     uint32 = 1508
)

// Provider 原始事件（1600-1699）。
const (
	EventTypeProviderRaw uint32 = 1600
)

// Workspace 运行事件（1700-1799）。
const (
	EventTypeWorkspaceRunCreated       uint32 = 1700
	EventTypeWorkspaceRunStatusChanged uint32 = 1701
	EventTypeWorkspaceRunMerged        uint32 = 1702
	EventTypeWorkspaceRunAborted       uint32 = 1703
	EventTypeWorkspaceRunMergeError    uint32 = 1704
)

// EventHeader 是所有类型事件共享的基础字段。
type EventHeader struct {
	Timestamp time.Time `json:"timestamp"`
}

// ThreadHeader 标识 thread 作用域的事件。
type ThreadHeader struct {
	EventHeader
	ThreadID string `json:"thread_id,omitempty"`
}

// AgentHeader 标识 agent 作用域的事件。
type AgentHeader struct {
	ThreadHeader
	AgentID string `json:"agent_id"`
}

// AgentSessionHeader 标识绑定到某次 agent session 的事件。
type AgentSessionHeader struct {
	AgentHeader
	SessionID string `json:"session_id,omitempty"`
}

// TurnIDHeader 在现有事件 scope 内标识具体的 turn。
type TurnIDHeader struct {
	TurnID string `json:"turn_id,omitempty"`
}

// TurnHeader 标识 turn 作用域的事件。
type TurnHeader struct {
	AgentHeader
	TurnIDHeader
}

// ToolCallHeader 标识 turn 内一次工具调用的事件。
type ToolCallHeader struct {
	TurnHeader
	CallID   string `json:"call_id"`
	ToolName string `json:"tool_name"`
}

// ToolApprovalHeader 标识某次工具调用的审批决策事件。
type ToolApprovalHeader struct {
	ToolCallHeader
	ApprovalID string `json:"approval_id,omitempty"`
}

// DAGHeader 标识 DAG 作用域的事件。
type DAGHeader struct {
	EventHeader
	DagKey string `json:"dag_key,omitempty"`
}

// TaskDAGHeader 标识 DAG 级别的任务事件。
type TaskDAGHeader struct {
	DAGHeader
}

// TaskNodeHeader 标识 DAG 节点级别的任务事件。
type TaskNodeHeader struct {
	TaskDAGHeader
	NodeKey string `json:"node_key"`
	RunID   int64  `json:"run_id,omitempty"`
	RunKey  string `json:"run_key,omitempty"`
}

// TaskWakeupHeader 标识 DAG wakeup 级别的任务事件。
type TaskWakeupHeader struct {
	TaskNodeHeader
	WakeupID int64 `json:"wakeup_id"`
}

// UIProjectionHeader 标识 UI 投影事件。
type UIProjectionHeader struct {
	ThreadHeader
	Projection string `json:"projection"`
}

// UITurnHeader 标识 turn 作用域的 UI 投影事件。
type UITurnHeader struct {
	UIProjectionHeader
	TurnIDHeader
}
