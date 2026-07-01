package dashboard

import (
	"context"
	"encoding/json"
	"time"
)

// AgentStatus 是 dashboard/agentStatus 对前端暴露的 agent 状态快照。
// 字段和 JSON 名称保持原 store DTO 形状，store 只在 module.go adapter 中转换。
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

// AILogFilter 是 dashboard 内部读取 AI 日志的过滤条件。
type AILogFilter struct {
	Keyword string
	Limit   int32
}

// AILog 是 dashboard/aiLogs 系列接口返回的 AI 日志 wire 条目。
// 该结构保留无 json tag 的字段命名，以兼容历史 store DTO 的编码输出。
type AILog struct {
	ID           int64
	Ts           time.Time
	Level        string
	Logger       string
	Message      string
	Raw          string
	Source       string
	Component    string
	AgentID      string
	ThreadID     string
	TraceID      string
	SpanID       string
	ParentSpanID string
	EventType    string
	ToolName     string
	DurationMs   *int32
	Extra        json.RawMessage
	Category     string
	Method       string
	URL          string
	Endpoint     string
	Status       string
	StatusText   string
	Model        string
}

// AILogStatusCount 是 dashboard/aiLogs/stats 返回的状态聚合条目。
type AILogStatusCount struct {
	Status string
	Count  int64
}

// LogDetailRequest 是 dashboard/logDetail 的内部请求。
type LogDetailRequest struct {
	Source string
	ID     int64
}

// LogDetail 是日志详情页暴露的安全投影，raw/extra 已脱敏并带截断诊断。
type LogDetail struct {
	Source         string          `json:"source"`
	ID             int64           `json:"id"`
	Timestamp      time.Time       `json:"timestamp"`
	Level          string          `json:"level,omitempty"`
	Logger         string          `json:"logger,omitempty"`
	Message        string          `json:"message,omitempty"`
	Raw            string          `json:"raw,omitempty"`
	RawTruncated   bool            `json:"raw_truncated,omitempty"`
	RawBytes       int64           `json:"raw_bytes,omitempty"`
	Component      string          `json:"component,omitempty"`
	AgentID        string          `json:"agent_id,omitempty"`
	ThreadID       string          `json:"thread_id,omitempty"`
	TraceID        string          `json:"trace_id,omitempty"`
	SpanID         string          `json:"span_id,omitempty"`
	ParentSpanID   string          `json:"parent_span_id,omitempty"`
	EventType      string          `json:"event_type,omitempty"`
	ToolName       string          `json:"tool_name,omitempty"`
	DurationMs     *int32          `json:"duration_ms,omitempty"`
	Extra          json.RawMessage `json:"extra,omitempty"`
	ExtraTruncated bool            `json:"extra_truncated,omitempty"`
	ExtraBytes     int64           `json:"extra_bytes,omitempty"`
}

// AuditLogFilter 是 dashboard/auditLogs 的查询条件。
type AuditLogFilter struct {
	EventType string
	Action    string
	Actor     string
	Keyword   string
	Limit     int32
}

// AuditEvent 是 dashboard/auditLogs 暴露的审计日志 wire 条目。
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

// BusLogFilter 是 dashboard/busLogs 的查询条件。
type BusLogFilter struct {
	Category string
	Severity string
	Keyword  string
	Limit    int32
}

// BusExceptionLog 是 dashboard/busLogs 暴露的业务异常日志 wire 条目。
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

// SystemLogFilter 是 dashboard 统一日志页读取 system log 的过滤条件。
type SystemLogFilter struct {
	Level        string
	Logger       string
	Source       string
	Component    string
	AgentID      string
	ThreadID     string
	TraceID      string
	SpanID       string
	ParentSpanID string
	EventType    string
	ToolName     string
	Keyword      string
	Limit        int32
}

// SystemLog 是 dashboard 内部映射到 LogEntry 的 system log 行。
// 该类型不直接暴露给前端，但字段名保持 store DTO，便于 adapter 逐字段转换。
type SystemLog struct {
	ID           int64
	Ts           time.Time
	Level        string
	Logger       string
	Message      string
	Raw          string
	Source       string
	Component    string
	AgentID      string
	ThreadID     string
	TraceID      string
	SpanID       string
	ParentSpanID string
	EventType    string
	ToolName     string
	DurationMs   *int32
	Extra        json.RawMessage
}

// CommandCardFilter 是 dashboard/commandCards 的查询条件。
type CommandCardFilter struct {
	Keyword string
	Limit   int32
}

// CommandCard 是 dashboard/commandCards 暴露的命令卡片 wire 条目。
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

// PromptTemplateFilter 是 dashboard/prompts 的查询条件。
type PromptTemplateFilter struct {
	AgentKey string
	Keyword  string
	CWD      string
	Limit    int32
}

// PromptTemplate 是 dashboard/prompts 和 ui/dashboard/get(commands) 暴露的 prompt wire 条目。
type PromptTemplate struct {
	ID             int64           `json:"id"`
	PromptKey      string          `json:"prompt_key"`
	Title          string          `json:"title"`
	AgentKey       string          `json:"agent_key"`
	ToolName       string          `json:"tool_name"`
	PromptText     string          `json:"prompt_text"`
	WhenToUse      string          `json:"when_to_use"`
	Variables      json.RawMessage `json:"variables"`
	Tags           json.RawMessage `json:"tags"`
	Enabled        bool            `json:"enabled"`
	ManuallyEdited bool            `json:"manually_edited"`
	MatchWhen      json.RawMessage `json:"match_when,omitempty"`
	Priority       int             `json:"priority"`
	CreatedBy      string          `json:"created_by"`
	UpdatedBy      string          `json:"updated_by"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Description    string          `json:"description"`
}

// SharedFileFilter 是 dashboard/sharedFiles 和 memory 页面的 sharedfile 查询条件。
type SharedFileFilter struct {
	Prefix string
	Limit  int32
}

// SharedFileUpsertParams 是 dashboard 写 workflow material 时传给 sharedfile writer 的参数。
type SharedFileUpsertParams struct {
	Path      string
	Content   string
	UpdatedBy string
}

// SharedFile 是 dashboard 暴露的 sharedfile wire 条目。
type SharedFile struct {
	Path      string    `json:"path"`
	Content   string    `json:"content"`
	UpdatedBy string    `json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AgentStatusReader 是 dashboard 读取 agent status 的窄 port。
type AgentStatusReader interface {
	List(ctx context.Context, status string) ([]AgentStatus, error)
}

// AILogReader 是 dashboard 读取 AI 日志和状态统计的窄 port。
type AILogReader interface {
	List(ctx context.Context, filter AILogFilter) ([]AILog, error)
	ListByCategory(ctx context.Context, category string, keyword string, limit int32) ([]AILog, error)
	CountByStatus(ctx context.Context) ([]AILogStatusCount, error)
	ListRecent(ctx context.Context, limit int32) ([]AILog, error)
}

// AuditLogReader 是 dashboard 读取审计日志的窄 port。
type AuditLogReader interface {
	List(ctx context.Context, filter AuditLogFilter) ([]AuditEvent, error)
}

// BusLogReader 是 dashboard 读取 bus 异常日志的窄 port。
type BusLogReader interface {
	List(ctx context.Context, filter BusLogFilter) ([]BusExceptionLog, error)
}

// SystemLogReader 是 dashboard 读取 system log 的窄 port。
type SystemLogReader interface {
	List(ctx context.Context, filter SystemLogFilter) ([]SystemLog, error)
}

// CommandCardReader 是 dashboard 读取命令卡片的窄 port。
type CommandCardReader interface {
	List(ctx context.Context, filter CommandCardFilter) ([]CommandCard, error)
}

// DBQueryExecutor 是 dashboard 透传只读 SQL 查询的窄 port。
type DBQueryExecutor interface {
	Query(ctx context.Context, query string, args ...any) ([]map[string]any, error)
}

// PromptTemplateReader 是 dashboard 读取 prompt 模板的窄 port。
type PromptTemplateReader interface {
	List(ctx context.Context, filter PromptTemplateFilter) ([]PromptTemplate, error)
}

// SharedFileReader 是 dashboard 读取 sharedfile 的窄 port。
type SharedFileReader interface {
	Get(ctx context.Context, path string) (*SharedFile, error)
	List(ctx context.Context, filter SharedFileFilter) ([]SharedFile, error)
}

// SharedFileWriter 是 dashboard 写入 workflow material sharedfile 的窄 port。
type SharedFileWriter interface {
	Upsert(ctx context.Context, params SharedFileUpsertParams) (*SharedFile, error)
}
