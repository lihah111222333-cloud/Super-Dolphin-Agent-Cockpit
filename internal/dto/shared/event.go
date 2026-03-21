package shared

import (
	"context"
	"strings"
	"time"
)

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

	EventTypeTaskDagCreated        uint32 = 1300
	EventTypeTaskNodeStatusChanged uint32 = 1301
	EventTypeTaskWakeupDispatched  uint32 = 1302
	EventTypeTaskWakeupCompleted   uint32 = 1303

	EventTypeThreadStarted      uint32 = 1350
	EventTypeThreadStopped      uint32 = 1351
	EventTypeThreadMessagesPage uint32 = 1352
	EventTypeThreadCompacted    uint32 = 1353

	EventTypeWorkspaceRunCreated       uint32 = 1400
	EventTypeWorkspaceRunStatusChanged uint32 = 1401
	EventTypeWorkspaceRunMerged        uint32 = 1402
	EventTypeWorkspaceRunAborted       uint32 = 1403
	EventTypeWorkspaceRunMergeError    uint32 = 1404

	EventTypeUIProjectionUpdated  uint32 = 1500
	EventTypeUITimelineAppended   uint32 = 1501
	EventTypeUITokensUpdated      uint32 = 1502
	EventTypeUISkillsChanged      uint32 = 1503
	EventTypeUIThreadPatch        uint32 = 1504
	EventTypeUIPreferencesChanged uint32 = 1505

	EventTypeProviderRaw uint32 = 1600
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
}

// TaskWakeupHeader identifies a DAG wakeup-scoped task event.
type TaskWakeupHeader struct {
	TaskNodeHeader
	WakeupID int64 `json:"wakeup_id"`
}

// WorkspaceRunHeader identifies a workspace run event.
type WorkspaceRunHeader struct {
	DAGHeader
	RunKey string `json:"run_key"`
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

type eventTimeKey struct{}

func WithEventTime(ctx context.Context, timestamp time.Time) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if timestamp.IsZero() {
		return ctx
	}
	return context.WithValue(ctx, eventTimeKey{}, timestamp)
}

func ResolveEventTime(ctx context.Context, payload map[string]any, fallbacks ...time.Time) time.Time {
	if timestamp := eventTimeFromContext(ctx); !timestamp.IsZero() {
		return timestamp
	}
	if timestamp := EventTimeFromPayload(payload); !timestamp.IsZero() {
		return timestamp
	}
	return FirstEventTime(fallbacks...)
}

func FirstEventTime(fallbacks ...time.Time) time.Time {
	for _, timestamp := range fallbacks {
		if !timestamp.IsZero() {
			return timestamp
		}
	}
	return time.Now()
}

func EventTimeFromPayload(payload map[string]any) time.Time {
	if len(payload) == 0 {
		return time.Time{}
	}
	return ParseEventTime(strings.TrimSpace(firstPayloadString(
		payload,
		"timestamp",
		"ts",
		"createdAt",
		"created_at",
		"updatedAt",
		"updated_at",
	)))
}

func ParseEventTime(raw string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func eventTimeFromContext(ctx context.Context) time.Time {
	if ctx == nil {
		return time.Time{}
	}
	timestamp, _ := ctx.Value(eventTimeKey{}).(time.Time)
	return timestamp
}

func firstPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}
