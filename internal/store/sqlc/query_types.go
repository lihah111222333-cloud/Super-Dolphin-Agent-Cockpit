package sqlc

import (
	"encoding/json"
	"time"
)

type ListRunningAgentsRow struct {
	ThreadID string `json:"thread_id"`
	Port     int32  `json:"port"`
	PID      int32  `json:"pid"`
	Status   string `json:"status"`
}

type AgentThreadCwdRow struct {
	ThreadID string `json:"thread_id"`
	Cwd      string `json:"cwd"`
}

type CwdLockHolderRow struct {
	InstanceID  string    `json:"instance_id"`
	PID         int32     `json:"pid"`
	HeartbeatAt time.Time `json:"heartbeat_at"`
}

type ListCommandCardsRow struct {
	ID              int64           `json:"id"`
	CardKey         string          `json:"card_key"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	CommandTemplate string          `json:"command_template"`
	ArgsSchema      json.RawMessage `json:"args_schema"`
	RiskLevel       string          `json:"risk_level"`
	Enabled         bool            `json:"enabled"`
	CreatedBy       string          `json:"created_by"`
	UpdatedBy       string          `json:"updated_by"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	LastRunAt       *time.Time      `json:"last_run_at"`
	RunCount        int64           `json:"run_count"`
}

type PlaceholderDBQueryRow struct {
	Placeholder *string `json:"placeholder"`
}

type InsertSystemLogParams struct {
	Level   string `json:"level"`
	Logger  string `json:"logger"`
	Message string `json:"message"`
	Raw     string `json:"raw"`
}

type ListSystemLogsParams struct {
	Level     string `json:"level"`
	Logger    string `json:"logger"`
	Source    string `json:"source"`
	Component string `json:"component"`
	AgentID   string `json:"agent_id"`
	ThreadID  string `json:"thread_id"`
	EventType string `json:"event_type"`
	ToolName  string `json:"tool_name"`
	Keyword   string `json:"keyword"`
	Limit     int32  `json:"limit"`
}

type InsertAuditEventParams struct {
	EventType string          `json:"event_type"`
	Action    string          `json:"action"`
	Result    string          `json:"result"`
	Actor     string          `json:"actor"`
	Target    string          `json:"target"`
	Detail    string          `json:"detail"`
	Level     string          `json:"level"`
	Extra     json.RawMessage `json:"extra"`
}

type ListAuditEventsParams struct {
	EventType string `json:"event_type"`
	Action    string `json:"action"`
	Actor     string `json:"actor"`
	Keyword   string `json:"keyword"`
	Limit     int32  `json:"limit"`
}

type ListAILogSystemLogsParams struct {
	Keyword string `json:"keyword"`
	Limit   int32  `json:"limit"`
}

type ListBusExceptionLogsParams struct {
	Category string `json:"category"`
	Severity string `json:"severity"`
	Keyword  string `json:"keyword"`
	Limit    int32  `json:"limit"`
}

type GetUIPreferenceValueParams struct {
	Cwd string `json:"cwd"`
	Key string `json:"key"`
}

type UpsertUIPreferenceParams struct {
	Cwd   string          `json:"cwd"`
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

type UpsertSharedFileParams struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	UpdatedBy string `json:"updated_by"`
}

type ListSharedFilesParams struct {
	Prefix string `json:"prefix"`
	Limit  int32  `json:"limit"`
}

type GetAgentProviderBindingByProviderThreadParams struct {
	Provider         string `json:"provider"`
	ProviderThreadID string `json:"provider_thread_id"`
}

type UpsertAgentProviderBindingParams struct {
	AgentID          string `json:"agent_id"`
	Provider         string `json:"provider"`
	ProviderThreadID string `json:"provider_thread_id"`
	CodexThreadID    string `json:"codex_thread_id"`
	RolloutPath      string `json:"rollout_path"`
	Cwd              string `json:"cwd"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
}

type UpdateAgentProviderBindingSessionUUIDParams struct {
	SessionUUID string `json:"session_uuid"`
	UpdatedAt   int64  `json:"updated_at"`
	AgentID     string `json:"agent_id"`
}

type UpdateAgentProviderBindingArchivedParams struct {
	Archived  bool   `json:"archived"`
	UpdatedAt int64  `json:"updated_at"`
	AgentID   string `json:"agent_id"`
}

type UpsertAgentThreadParams struct {
	ThreadID  string `json:"thread_id"`
	Prompt    string `json:"prompt"`
	Model     string `json:"model"`
	Cwd       string `json:"cwd"`
	Status    string `json:"status"`
	Port      int32  `json:"port"`
	PID       int32  `json:"pid"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type UpdateAgentThreadStatusParams struct {
	ThreadID  string `json:"thread_id"`
	Status    string `json:"status"`
	UpdatedAt int64  `json:"updated_at"`
}

type DeleteAgentThreadByIDParams struct {
	ThreadID string `json:"thread_id"`
}

type ExpireStaleAgentThreadsParams struct {
	UpdatedAt int64 `json:"updated_at"`
	Cutoff    int64 `json:"cutoff"`
}

type UpsertAgentStatusParams struct {
	AgentID     string          `json:"agent_id"`
	AgentName   string          `json:"agent_name"`
	SessionID   string          `json:"session_id"`
	Status      string          `json:"status"`
	StagnantSec int32           `json:"stagnant_sec"`
	Error       string          `json:"error"`
	OutputTail  json.RawMessage `json:"output_tail"`
}

type AcquireCwdLockParams struct {
	Cwd        string `json:"cwd"`
	InstanceID string `json:"instance_id"`
	PID        int32  `json:"pid"`
}

type ForceAcquireCwdLockParams struct {
	Cwd        string `json:"cwd"`
	InstanceID string `json:"instance_id"`
	PID        int32  `json:"pid"`
	HolderPID  int32  `json:"holder_pid"`
}

type ReleaseCwdLockParams struct {
	Cwd        string `json:"cwd"`
	InstanceID string `json:"instance_id"`
}

type HeartbeatCwdLockParams struct {
	Cwd        string `json:"cwd"`
	InstanceID string `json:"instance_id"`
	PID        int32  `json:"pid"`
}

type UpsertTaskAckParams struct {
	AckKey        string          `json:"ack_key"`
	Title         string          `json:"title"`
	Description   string          `json:"description"`
	AssignedTo    string          `json:"assigned_to"`
	RequestedBy   string          `json:"requested_by"`
	Priority      string          `json:"priority"`
	Status        string          `json:"status"`
	Progress      int32           `json:"progress"`
	AckMessage    string          `json:"ack_message"`
	ResultSummary string          `json:"result_summary"`
	Metadata      json.RawMessage `json:"metadata"`
	DueAt         *time.Time      `json:"due_at"`
}

type ListTaskAcksParams struct {
	Status     string `json:"status"`
	Priority   string `json:"priority"`
	AssignedTo string `json:"assigned_to"`
	Keyword    string `json:"keyword"`
	Limit      int32  `json:"limit"`
}

type UpsertTaskDagParams struct {
	DagKey      string          `json:"dag_key"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Status      string          `json:"status"`
	CreatedBy   string          `json:"created_by"`
	Metadata    json.RawMessage `json:"metadata"`
}

type ListTaskDagsParams struct {
	Status  string `json:"status"`
	Keyword string `json:"keyword"`
	Limit   int32  `json:"limit"`
}

type UpsertTaskDagNodeParams struct {
	DagKey     string          `json:"dag_key"`
	NodeKey    string          `json:"node_key"`
	Title      string          `json:"title"`
	NodeType   string          `json:"node_type"`
	AssignedTo string          `json:"assigned_to"`
	DependsOn  json.RawMessage `json:"depends_on"`
	CommandRef string          `json:"command_ref"`
	Config     json.RawMessage `json:"config"`
}

type UpdateTaskDagNodeStatusParams struct {
	Status  string          `json:"status"`
	Result  json.RawMessage `json:"result"`
	DagKey  string          `json:"dag_key"`
	NodeKey string          `json:"node_key"`
}

type BindRunningTaskDagNodeTurnParams struct {
	TurnID   string `json:"turn_id"`
	DagKey   string `json:"dag_key"`
	NodeKey  string `json:"node_key"`
	WakeupID int64  `json:"wakeup_id"`
}

type TouchRunningTaskDagNodeEventParams struct {
	ObservedAt time.Time `json:"observed_at"`
	DagKey     string    `json:"dag_key"`
	NodeKey    string    `json:"node_key"`
	TurnID     string    `json:"turn_id"`
}

type UpdateRunningTaskDagNodeStatusParams struct {
	Status   string          `json:"status"`
	Result   json.RawMessage `json:"result"`
	WakeupID int64           `json:"wakeup_id"`
	DagKey   string          `json:"dag_key"`
	NodeKey  string          `json:"node_key"`
}

type UpdateAwaitingVerifyTaskDagNodeStatusParams struct {
	Status  string          `json:"status"`
	Result  json.RawMessage `json:"result"`
	DagKey  string          `json:"dag_key"`
	NodeKey string          `json:"node_key"`
}

type CompleteTaskDagNodeParams struct {
	Status  string          `json:"status"`
	Result  json.RawMessage `json:"result"`
	DagKey  string          `json:"dag_key"`
	NodeKey string          `json:"node_key"`
}

type UpdateTaskDagNodeStatusFlexibleParams struct {
	Status  string          `json:"status"`
	Result  json.RawMessage `json:"result"`
	DagKey  string          `json:"dag_key"`
	NodeKey string          `json:"node_key"`
}

type EnqueueTaskDagWakeupParams struct {
	DagKey         string          `json:"dag_key"`
	NodeKey        string          `json:"node_key"`
	WakeupKind     string          `json:"wakeup_kind"`
	TargetAgentID  string          `json:"target_agent_id"`
	PromptPayload  json.RawMessage `json:"prompt_payload"`
	IdempotencyKey string          `json:"idempotency_key"`
}

type ClaimDueTaskDagWakeupsParams struct {
	ClaimedBy     string `json:"claimed_by"`
	LeaseInterval string `json:"lease_interval"`
	Limit         int32  `json:"limit"`
}

type MarkTaskDagWakeupSentParams struct {
	ID        int64     `json:"id"`
	ClaimedAt time.Time `json:"claimed_at"`
}

type BindTaskDagWakeupTurnParams struct {
	TurnID string `json:"turn_id"`
	ID     int64  `json:"id"`
}

type RetryTaskDagWakeupParams struct {
	RetryInterval string    `json:"retry_interval"`
	LastError     string    `json:"last_error"`
	ID            int64     `json:"id"`
	ClaimedAt     time.Time `json:"claimed_at"`
}

type FailTaskDagWakeupParams struct {
	LastError string    `json:"last_error"`
	ID        int64     `json:"id"`
	ClaimedAt time.Time `json:"claimed_at"`
}

type AcquireTaskDagWorkerLeaseParams struct {
	TargetAgentID string `json:"target_agent_id"`
	OwnerID       string `json:"owner_id"`
	LeaseInterval string `json:"lease_interval"`
}

type RenewTaskDagWorkerLeaseParams struct {
	LeaseInterval string `json:"lease_interval"`
	TargetAgentID string `json:"target_agent_id"`
	OwnerID       string `json:"owner_id"`
}

type ReleaseTaskDagWorkerLeaseParams struct {
	TargetAgentID string `json:"target_agent_id"`
	OwnerID       string `json:"owner_id"`
}

type InsertTaskTraceParams struct {
	TraceID       string          `json:"trace_id"`
	SpanID        string          `json:"span_id"`
	ParentSpanID  string          `json:"parent_span_id"`
	SpanName      string          `json:"span_name"`
	Component     string          `json:"component"`
	InputPayload  json.RawMessage `json:"input_payload"`
	OutputPayload json.RawMessage `json:"output_payload"`
	Status        string          `json:"status"`
	ErrorText     string          `json:"error_text"`
	DurationMs    int32           `json:"duration_ms"`
	Metadata      json.RawMessage `json:"metadata"`
}

type ListTaskTracesParams struct {
	Component string     `json:"component"`
	Since     *time.Time `json:"since"`
	Keyword   string     `json:"keyword"`
	Limit     int32      `json:"limit"`
}
