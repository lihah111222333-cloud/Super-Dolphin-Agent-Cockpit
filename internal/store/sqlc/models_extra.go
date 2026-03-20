package sqlc

import (
	"encoding/json"
	"time"
)

type SchemaMigration struct {
	Version   int32     `json:"version"`
	Name      string    `json:"name"`
	Filename  string    `json:"filename"`
	AppliedAt time.Time `json:"applied_at"`
}

type SharedFile struct {
	Path      string    `json:"path"`
	Content   string    `json:"content"`
	UpdatedBy string    `json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type SystemLog struct {
	ID         int64           `json:"id"`
	Ts         time.Time       `json:"ts"`
	Level      string          `json:"level"`
	Logger     string          `json:"logger"`
	Message    string          `json:"message"`
	Raw        string          `json:"raw"`
	Source     string          `json:"source"`
	Component  string          `json:"component"`
	AgentID    string          `json:"agent_id"`
	ThreadID   string          `json:"thread_id"`
	TraceID    string          `json:"trace_id"`
	EventType  string          `json:"event_type"`
	ToolName   string          `json:"tool_name"`
	DurationMs *int32          `json:"duration_ms"`
	Extra      json.RawMessage `json:"extra"`
}

type TaskAck struct {
	ID            int64           `json:"id"`
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
	AckedAt       *time.Time      `json:"acked_at"`
	StartedAt     *time.Time      `json:"started_at"`
	FinishedAt    *time.Time      `json:"finished_at"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type TaskDagNode struct {
	ID             int64           `json:"id"`
	DagKey         string          `json:"dag_key"`
	NodeKey        string          `json:"node_key"`
	Title          string          `json:"title"`
	NodeType       string          `json:"node_type"`
	AssignedTo     string          `json:"assigned_to"`
	DependsOn      json.RawMessage `json:"depends_on"`
	Status         string          `json:"status"`
	CommandRef     string          `json:"command_ref"`
	Config         json.RawMessage `json:"config"`
	Result         json.RawMessage `json:"result"`
	StartedAt      *time.Time      `json:"started_at"`
	FinishedAt     *time.Time      `json:"finished_at"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	ActiveTurnID   *string         `json:"active_turn_id"`
	ActiveWakeupID *int64          `json:"active_wakeup_id"`
	LastEventAt    *time.Time      `json:"last_event_at"`
}

type TaskDag struct {
	ID          int64           `json:"id"`
	DagKey      string          `json:"dag_key"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Status      string          `json:"status"`
	CreatedBy   string          `json:"created_by"`
	Metadata    json.RawMessage `json:"metadata"`
	StartedAt   *time.Time      `json:"started_at"`
	FinishedAt  *time.Time      `json:"finished_at"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type TaskDagWakeup struct {
	ID             int64           `json:"id"`
	DagKey         string          `json:"dag_key"`
	NodeKey        string          `json:"node_key"`
	WakeupKind     string          `json:"wakeup_kind"`
	TargetAgentID  string          `json:"target_agent_id"`
	PromptPayload  json.RawMessage `json:"prompt_payload"`
	IdempotencyKey string          `json:"idempotency_key"`
	Status         string          `json:"status"`
	AttemptCount   int32           `json:"attempt_count"`
	NextRetryAt    time.Time       `json:"next_retry_at"`
	ClaimedAt      *time.Time      `json:"claimed_at"`
	ClaimedBy      string          `json:"claimed_by"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at"`
	SentAt         *time.Time      `json:"sent_at"`
	BoundTurnID    *string         `json:"bound_turn_id"`
	TurnBoundAt    *time.Time      `json:"turn_bound_at"`
	LastError      string          `json:"last_error"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type TaskDagWorkerLease struct {
	TargetAgentID  string    `json:"target_agent_id"`
	OwnerID        string    `json:"owner_id"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type TaskTrace struct {
	ID            int64           `json:"id"`
	TraceID       string          `json:"trace_id"`
	SpanID        string          `json:"span_id"`
	ParentSpanID  string          `json:"parent_span_id"`
	SpanName      string          `json:"span_name"`
	Component     string          `json:"component"`
	Status        string          `json:"status"`
	InputPayload  json.RawMessage `json:"input_payload"`
	OutputPayload json.RawMessage `json:"output_payload"`
	ErrorText     string          `json:"error_text"`
	Metadata      json.RawMessage `json:"metadata"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    *time.Time      `json:"finished_at"`
	DurationMs    int32           `json:"duration_ms"`
}

type TopologyApprovalArchive struct {
	ID                   string          `json:"id"`
	Status               string          `json:"status"`
	RequestedBy          string          `json:"requested_by"`
	Reason               string          `json:"reason"`
	CreatedAt            time.Time       `json:"created_at"`
	ExpireAt             time.Time       `json:"expire_at"`
	ReviewedAt           *time.Time      `json:"reviewed_at"`
	Reviewer             string          `json:"reviewer"`
	ReviewNote           string          `json:"review_note"`
	ArchHash             string          `json:"arch_hash"`
	ProposedArchitecture json.RawMessage `json:"proposed_architecture"`
	ArchivedAt           time.Time       `json:"archived_at"`
}

type TopologyApproval struct {
	ID                   string          `json:"id"`
	Status               string          `json:"status"`
	RequestedBy          string          `json:"requested_by"`
	Reason               string          `json:"reason"`
	CreatedAt            time.Time       `json:"created_at"`
	ExpireAt             time.Time       `json:"expire_at"`
	ReviewedAt           *time.Time      `json:"reviewed_at"`
	Reviewer             string          `json:"reviewer"`
	ReviewNote           string          `json:"review_note"`
	ArchHash             string          `json:"arch_hash"`
	ProposedArchitecture json.RawMessage `json:"proposed_architecture"`
}

type UIPreference struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	UpdatedAt time.Time       `json:"updated_at"`
	Cwd       string          `json:"cwd"`
}

type WorkspaceRunFile struct {
	ID                 int64     `json:"id"`
	RunKey             string    `json:"run_key"`
	RelativePath       string    `json:"relative_path"`
	BaselineSHA256     string    `json:"baseline_sha256"`
	WorkspaceSHA256    string    `json:"workspace_sha256"`
	SourceSHA256Before string    `json:"source_sha256_before"`
	SourceSHA256After  string    `json:"source_sha256_after"`
	State              string    `json:"state"`
	LastError          string    `json:"last_error"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type WorkspaceRun struct {
	ID            int64           `json:"id"`
	RunKey        string          `json:"run_key"`
	DagKey        string          `json:"dag_key"`
	SourceRoot    string          `json:"source_root"`
	WorkspacePath string          `json:"workspace_path"`
	Status        string          `json:"status"`
	CreatedBy     string          `json:"created_by"`
	UpdatedBy     string          `json:"updated_by"`
	Metadata      json.RawMessage `json:"metadata"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	FinishedAt    *time.Time      `json:"finished_at"`
}
