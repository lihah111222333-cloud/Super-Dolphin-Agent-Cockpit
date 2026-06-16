package contract

import (
	"context"
	"encoding/json"
	"time"
)

// AgentStatusStore persists and lists current agent runtime statuses.
type AgentStatusStore interface {
	Upsert(ctx context.Context, params AgentStatusUpsertParams) (*AgentStatus, error)
	Get(ctx context.Context, agentID string) (*AgentStatus, error)
	List(ctx context.Context, status string) ([]AgentStatus, error)
}

// AgentStatusUpsertParams updates the current status for one agent.
type AgentStatusUpsertParams struct {
	AgentID     string
	AgentName   string
	SessionID   string
	Status      string
	StagnantSec int32
	Error       string
	OutputTail  json.RawMessage
}

// AgentStatus is the dashboard projection of one agent runtime.
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

// AILogStore reads normalized AI/runtime logs for dashboards.
type AILogStore interface {
	List(ctx context.Context, filter AILogListFilter) ([]AILog, error)
	ListByCategory(ctx context.Context, category string, keyword string, limit int32) ([]AILog, error)
	CountByStatus(ctx context.Context) ([]AILogStatusCount, error)
	ListRecent(ctx context.Context, limit int32) ([]AILog, error)
}

// AILogListFilter constrains AI log list queries.
type AILogListFilter struct {
	Keyword string
	Limit   int32
}

// AILog is a normalized log row used by operational dashboards.
type AILog struct {
	ID         int64
	Ts         time.Time
	Level      string
	Logger     string
	Message    string
	Raw        string
	Source     string
	Component  string
	AgentID    string
	ThreadID   string
	TraceID    string
	EventType  string
	ToolName   string
	DurationMs *int32
	Extra      json.RawMessage
	Category   string
	Method     string
	URL        string
	Endpoint   string
	Status     string
	StatusText string
	Model      string
}

// AILogStatusCount summarizes AI logs by status.
type AILogStatusCount struct {
	Status string
	Count  int64
}

// AuditLogStore reads and writes audit events.
type AuditLogStore interface {
	List(ctx context.Context, filter AuditLogListFilter) ([]AuditEvent, error)
	Insert(ctx context.Context, params AuditLogInsertParams) error
}

// AuditLogListFilter constrains audit event list queries.
type AuditLogListFilter struct {
	EventType string
	Action    string
	Actor     string
	Keyword   string
	Limit     int32
}

// AuditLogInsertParams captures one audit event write.
type AuditLogInsertParams struct {
	EventType string
	Action    string
	Result    string
	Actor     string
	Target    string
	Detail    string
	Level     string
	Extra     json.RawMessage
}

// AuditEvent is a normalized audit event row.
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

// BusLogStore reads exception logs from the internal event bus.
type BusLogStore interface {
	List(ctx context.Context, filter BusLogListFilter) ([]BusExceptionLog, error)
}

// BusLogListFilter constrains bus exception log queries.
type BusLogListFilter struct {
	Category string
	Severity string
	Keyword  string
	Limit    int32
}

// BusExceptionLog is a dashboard projection of one bus exception.
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

// SystemLogStore reads and writes system log rows.
type SystemLogStore interface {
	List(ctx context.Context, filter SystemLogListFilter) ([]SystemLog, error)
	Insert(ctx context.Context, params SystemLogInsertParams) error
}

// SystemLogListFilter constrains system log list queries.
type SystemLogListFilter struct {
	Level     string
	Logger    string
	Source    string
	Component string
	AgentID   string
	ThreadID  string
	EventType string
	ToolName  string
	Keyword   string
	Limit     int32
}

// SystemLogInsertParams captures one raw system log write.
type SystemLogInsertParams struct {
	Level   string
	Logger  string
	Message string
	Raw     string
}

// SystemLog is a normalized system log row.
type SystemLog struct {
	ID         int64
	Ts         time.Time
	Level      string
	Logger     string
	Message    string
	Raw        string
	Source     string
	Component  string
	AgentID    string
	ThreadID   string
	TraceID    string
	EventType  string
	ToolName   string
	DurationMs *int32
	Extra      json.RawMessage
}

// DBQueryStore executes dashboard diagnostic database queries.
type DBQueryStore interface {
	Placeholder(ctx context.Context) ([]DBQueryPlaceholderRow, error)
	Query(ctx context.Context, query string, args ...any) ([]map[string]any, error)
}

// DBQueryPlaceholderRow is the minimal placeholder query projection.
type DBQueryPlaceholderRow struct {
	Placeholder *string
}

// CommandCardReader provides read-only access to command cards.
type CommandCardReader interface {
	List(ctx context.Context, filter CommandCardListFilter) ([]CommandCard, error)
}

// CommandCardListFilter constrains command-card list queries.
type CommandCardListFilter struct {
	Keyword string
	Limit   int32
}

// CommandCard is the dashboard projection of an executable command card.
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
	LastRunAt       *time.Time      `json:"last_run_at,omitempty"`
	RunCount        int64           `json:"run_count"`
}
