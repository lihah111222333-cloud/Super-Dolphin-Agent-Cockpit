package sqlc

import (
	"encoding/json"
	"time"
)

type AgentCodexBinding struct {
	AgentID       string `json:"agent_id"`
	CodexThreadID string `json:"codex_thread_id"`
	RolloutPath   string `json:"rollout_path"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
	Cwd           string `json:"cwd"`
	Archived      bool   `json:"archived"`
}

type AgentInteraction struct {
	ID             int64           `json:"id"`
	ThreadID       string          `json:"thread_id"`
	ParentID       *int64          `json:"parent_id"`
	Sender         string          `json:"sender"`
	Receiver       string          `json:"receiver"`
	MsgType        string          `json:"msg_type"`
	Status         string          `json:"status"`
	RequiresReview bool            `json:"requires_review"`
	ReviewedBy     string          `json:"reviewed_by"`
	ReviewNote     string          `json:"review_note"`
	ReviewedAt     *time.Time      `json:"reviewed_at"`
	Payload        json.RawMessage `json:"payload"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type AgentProviderBinding struct {
	AgentID          string `json:"agent_id"`
	Provider         string `json:"provider"`
	ProviderThreadID string `json:"provider_thread_id"`
	CodexThreadID    string `json:"codex_thread_id"`
	RolloutPath      string `json:"rollout_path"`
	Cwd              string `json:"cwd"`
	Archived         bool   `json:"archived"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
	SessionUUID      string `json:"session_uuid"`
}

type AgentStatus struct {
	AgentID     string          `json:"agent_id"`
	AgentName   string          `json:"agent_name"`
	SessionID   string          `json:"session_id"`
	Status      string          `json:"status"`
	StagnantSec int32           `json:"stagnant_sec"`
	Error       string          `json:"error"`
	OutputTail  json.RawMessage `json:"output_tail"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type AgentThread struct {
	ThreadID        string `json:"thread_id"`
	AgentID         string `json:"agent_id"`
	Prompt          string `json:"prompt"`
	Model           string `json:"model"`
	Cwd             string `json:"cwd"`
	Status          string `json:"status"`
	Port            int32  `json:"port"`
	PID             int32  `json:"pid"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
	FinishedAt      *int64 `json:"finished_at"`
	LastEventType   string `json:"last_event_type"`
	ErrorMessage    string `json:"error_message"`
	WorkspaceRunKey string `json:"workspace_run_key"`
	OwnerThreadID   string `json:"owner_thread_id"`
}

type AuditEvent struct {
	ID        int64           `json:"id"`
	Ts        time.Time       `json:"ts"`
	EventType string          `json:"event_type"`
	Action    string          `json:"action"`
	Result    string          `json:"result"`
	Actor     string          `json:"actor"`
	Target    string          `json:"target"`
	Detail    string          `json:"detail"`
	Level     string          `json:"level"`
	Extra     json.RawMessage `json:"extra"`
}

type BusExceptionLog struct {
	ID        int64           `json:"id"`
	Ts        time.Time       `json:"ts"`
	Category  string          `json:"category"`
	Severity  string          `json:"severity"`
	Source    string          `json:"source"`
	ToolName  string          `json:"tool_name"`
	Message   string          `json:"message"`
	Traceback string          `json:"traceback"`
	Extra     json.RawMessage `json:"extra"`
}

type CommandCardRun struct {
	ID              int64           `json:"id"`
	CardKey         string          `json:"card_key"`
	RequestedBy     string          `json:"requested_by"`
	Params          json.RawMessage `json:"params"`
	RenderedCommand string          `json:"rendered_command"`
	RiskLevel       string          `json:"risk_level"`
	Status          string          `json:"status"`
	RequiresReview  bool            `json:"requires_review"`
	InteractionID   *int64          `json:"interaction_id"`
	Output          string          `json:"output"`
	Error           string          `json:"error"`
	ExitCode        *int32          `json:"exit_code"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	ExecutedAt      *time.Time      `json:"executed_at"`
}

type CommandCardVersion struct {
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
	SourceUpdatedAt *time.Time      `json:"source_updated_at"`
	CreatedAt       time.Time       `json:"created_at"`
	ArchivedAt      time.Time       `json:"archived_at"`
}

type CommandCard struct {
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
}

type CwdInstanceLock struct {
	Cwd         string    `json:"cwd"`
	InstanceID  string    `json:"instance_id"`
	PID         int32     `json:"pid"`
	AcquiredAt  time.Time `json:"acquired_at"`
	HeartbeatAt time.Time `json:"heartbeat_at"`
}

type PromptTemplateVersion struct {
	ID              int64           `json:"id"`
	PromptKey       string          `json:"prompt_key"`
	Title           string          `json:"title"`
	AgentKey        string          `json:"agent_key"`
	ToolName        string          `json:"tool_name"`
	PromptText      string          `json:"prompt_text"`
	Variables       json.RawMessage `json:"variables"`
	Tags            json.RawMessage `json:"tags"`
	Enabled         bool            `json:"enabled"`
	CreatedBy       string          `json:"created_by"`
	UpdatedBy       string          `json:"updated_by"`
	SourceUpdatedAt *time.Time      `json:"source_updated_at"`
	CreatedAt       time.Time       `json:"created_at"`
	ArchivedAt      time.Time       `json:"archived_at"`
}

type PromptVersion struct {
	ID              int64           `json:"id"`
	PromptKey       string          `json:"prompt_key"`
	Title           string          `json:"title"`
	AgentKey        string          `json:"agent_key"`
	ToolName        string          `json:"tool_name"`
	PromptText      string          `json:"prompt_text"`
	Variables       json.RawMessage `json:"variables"`
	Tags            json.RawMessage `json:"tags"`
	Enabled         bool            `json:"enabled"`
	CreatedBy       string          `json:"created_by"`
	UpdatedBy       string          `json:"updated_by"`
	SourceUpdatedAt *time.Time      `json:"source_updated_at"`
	CreatedAt       time.Time       `json:"created_at"`
	ArchivedAt      time.Time       `json:"archived_at"`
}

type PromptTemplate struct {
	ID          int64           `json:"id"`
	PromptKey   string          `json:"prompt_key"`
	Title       string          `json:"title"`
	AgentKey    string          `json:"agent_key"`
	ToolName    string          `json:"tool_name"`
	PromptText  string          `json:"prompt_text"`
	Variables   json.RawMessage `json:"variables"`
	Tags        json.RawMessage `json:"tags"`
	Enabled     bool            `json:"enabled"`
	CreatedBy   string          `json:"created_by"`
	UpdatedBy   string          `json:"updated_by"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	Description string          `json:"description"`
}

type Prompt struct {
	ID         int64     `json:"id"`
	AgentKey   string    `json:"agent_key"`
	ToolName   string    `json:"tool_name"`
	PromptText string    `json:"prompt_text"`
	IsPinned   bool      `json:"is_pinned"`
	SortOrder  int32     `json:"sort_order"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}
