package mcp

import "encoding/json"

// LeaseKey 是已注册生命周期租约的规范身份标识，InstanceID+Generation 唯一确定一次注册。
type LeaseKey struct {
	InstanceID string `json:"instance_id"` // 进程实例唯一标识。
	Generation uint64 `json:"generation"`  // 本次注册的世代号，用于防止旧请求覆盖新状态。
}

// RegisterRequest 是 ctl/register 的注册请求载荷，peer 启动后发送以建立租约。
type RegisterRequest struct {
	InstanceID           string   `json:"instance_id"`
	BinaryName           string   `json:"binary_name"`
	AgentID              string   `json:"agent_id"`
	ThreadID             string   `json:"thread_id,omitempty"`
	PID                  int      `json:"pid"`
	SessionToken         string   `json:"session_token,omitempty"`
	BootID               string   `json:"boot_id,omitempty"`
	ClientKind           string   `json:"client_kind"` // orch/lsp/ida/custom
	PeerKind             string   `json:"peer_kind,omitempty"`
	Shared               bool     `json:"shared,omitempty"`
	CapabilitiesOffered  []string `json:"capabilities_offered,omitempty"`
	CapabilitiesRequired []string `json:"capabilities_required,omitempty"`
	Subscriptions        []string `json:"subscriptions,omitempty"`
	ResumeFromGeneration *uint64  `json:"resume_from_generation,omitempty"`
}

// RegisterResponse 是 ctl/register 的响应，包含服务端分配的世代号和心跳参数。
type RegisterResponse struct {
	InstanceID             string   `json:"instance_id"`
	Generation             uint64   `json:"generation"`
	AcceptedGeneration     uint64   `json:"accepted_generation,omitempty"`
	PeerKind               string   `json:"peer_kind,omitempty"`
	CapabilitiesNegotiated []string `json:"capabilities_negotiated,omitempty"`
	CapabilitiesRejected   []string `json:"capabilities_rejected"`
	HeartbeatIntervalMs    int      `json:"heartbeat_interval_ms"`
	HeartbeatTimeoutMs     int      `json:"heartbeat_timeout_ms"`
	SendTimeoutMs          int      `json:"send_timeout_ms,omitempty"`
	SweeperIntervalMs      int      `json:"sweeper_interval_ms,omitempty"`
	ServerProtocolVersion  string   `json:"server_protocol_version,omitempty"`
	ConfigVersion          int64    `json:"config_version"`
}

// HeartbeatRequest 是 ctl/heartbeat 的心跳请求载荷，peer 按协商间隔定期发送。
type HeartbeatRequest struct {
	InstanceID            string          `json:"instance_id"`
	Generation            uint64          `json:"generation"`
	HeartbeatSeq          uint64          `json:"heartbeat_seq"`
	Status                string          `json:"status,omitempty"`
	Metrics               json.RawMessage `json:"metrics,omitempty"`
	ObservedConfigVersion int64           `json:"observed_config_version,omitempty"`
}

// HeartbeatResponse 是 ctl/heartbeat 的响应，服务端确认心跳并返回当前配置版本。
type HeartbeatResponse struct {
	OK              bool  `json:"ok"`
	ServerTime      int64 `json:"server_time"`
	ConfigVersion   int64 `json:"config_version"`
	NextHeartbeatMs int   `json:"next_heartbeat_ms,omitempty"`
}

// ContextRequest 是 ctl/context 的请求载荷，peer 通过 scope 拉取指定上下文快照。
type ContextRequest struct {
	InstanceID string   `json:"instance_id"`
	Generation uint64   `json:"generation"`
	AgentID    string   `json:"agent_id,omitempty"`
	Scope      string   `json:"scope"` // agent.runtime / thread.binding / workspace.run / config.snapshot
	Keys       []string `json:"keys,omitempty"`
}

// ContextResponse 是 ctl/context 的响应，包含 scope 快照内容和数据来源标识。
type ContextResponse struct {
	Source     string          `json:"source"` // live / boot_snapshot / db_rebuild
	ObservedAt int64           `json:"observed_at"`
	Scope      string          `json:"scope,omitempty"`
	Payload    json.RawMessage `json:"payload"`
	Data       json.RawMessage `json:"data,omitempty"`
}

// EventNotify 是 ctl/event 的通知载荷，peer 向服务端推送业务事件。
type EventNotify struct {
	InstanceID string          `json:"instance_id"`
	Generation uint64          `json:"generation"`
	EventID    string          `json:"event_id,omitempty"`
	EventType  string          `json:"event_type"`
	AuditClass string          `json:"audit_class,omitempty"`
	Payload    json.RawMessage `json:"payload"`
}

// LogNotify 是 ctl/log 的通知载荷，peer 向服务端推送结构化日志条目。
type LogNotify struct {
	InstanceID string         `json:"instance_id"`
	Generation uint64         `json:"generation"`
	Seq        uint64         `json:"seq,omitempty"`
	Level      string         `json:"level"`
	Message    string         `json:"message"`
	Fields     map[string]any `json:"fields,omitempty"`
	TS         int64          `json:"ts,omitempty"`
}

// ApprovalRequest 是 ctl/approval/request 的请求载荷，peer 请求对工具调用进行审批。
type ApprovalRequest struct {
	InstanceID string          `json:"instance_id"`
	Generation uint64          `json:"generation"`
	CallID     string          `json:"call_id"`
	ToolName   string          `json:"tool_name"`
	Reason     string          `json:"reason"`
	Kind       string          `json:"kind"`
	Payload    json.RawMessage `json:"payload"`
	TimeoutMs  int             `json:"timeout_ms,omitempty"`
}

// RuntimeReport 是 ctl/report 的 runtime 变体，记录 peer 运行时端口和 provider 信息。
type RuntimeReport struct {
	Port     int    `json:"port,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// CompletionReport 是 ctl/report 的 completion 变体，记录 agent turn 的终态和元数据。
type CompletionReport struct {
	Status   string          `json:"status"`
	Report   string          `json:"report,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// ProgressReport 是 ctl/report 的 progress 变体，保留字段，用于未来进度上报。
type ProgressReport struct {
	Status   string   `json:"status,omitempty"`
	Message  string   `json:"message,omitempty"`
	Percent  *float64 `json:"percent,omitempty"`
	Step     string   `json:"step,omitempty"`
	Sequence uint64   `json:"sequence,omitempty"`
}

// DiagnosticReport 是 ctl/report 的 diagnostic 变体，保留字段，用于未来诊断上报。
type DiagnosticReport struct {
	Level   string          `json:"level,omitempty"`
	Code    string          `json:"code,omitempty"`
	Message string          `json:"message,omitempty"`
	Details json.RawMessage `json:"details,omitempty"`
}

// ReportEnvelope 是 ctl/report 的判别联合载荷，Type 字段决定哪个变体非空。
type ReportEnvelope struct {
	Type       string            `json:"type"`
	Runtime    *RuntimeReport    `json:"runtime,omitempty"`
	Completion *CompletionReport `json:"completion,omitempty"`
	Progress   *ProgressReport   `json:"progress,omitempty"`
	Diagnostic *DiagnosticReport `json:"diagnostic,omitempty"`
}

// ReportRequest 是 ctl/report 的请求载荷，ReportID 用于幂等去重。
type ReportRequest struct {
	InstanceID string         `json:"instance_id"`
	Generation uint64         `json:"generation"`
	ReportID   string         `json:"report_id"` // idempotency key
	Report     ReportEnvelope `json:"report"`
}

// ReportResponse 是 ctl/report 的响应，确认报告是否已被接受并持久化。
type ReportResponse struct {
	Accepted        bool   `json:"accepted"`
	Success         bool   `json:"success,omitempty"`
	PersistedAt     int64  `json:"persisted_at,omitempty"`
	CanonicalStatus string `json:"canonical_status,omitempty"`
	AppliedVariant  string `json:"applied_variant,omitempty"`
}

// ShutdownRequest 是 ctl/shutdown 的请求载荷，服务端指示 peer 有序退出。
type ShutdownRequest struct {
	InstanceID          string `json:"instance_id"`
	Generation          uint64 `json:"generation"`
	ShutdownID          string `json:"shutdown_id"`
	Reason              string `json:"reason"`
	TimeoutMs           int    `json:"timeout_ms,omitempty"`
	DeadlineMs          int    `json:"deadline_ms,omitempty"`
	FinalReportExpected bool   `json:"final_report_expected,omitempty"`
}

// SelectorScope 是 config Selector 内的目标 scope 过滤条件。
type SelectorScope struct {
	AgentID    string `json:"agent_id,omitempty"`
	ThreadID   string `json:"thread_id,omitempty"`
	ClientKind string `json:"client_kind,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
}

// Selector 按 subscription、capability、scope 三者交集过滤 config fanout 目标。
type Selector struct {
	Subscription string         `json:"subscription,omitempty"`
	Capability   string         `json:"capability,omitempty"`
	Scope        *SelectorScope `json:"scope,omitempty"`
}

// ConfigChangedNotify 是 ctl/config/changed 的通知载荷，携带配置变更的版本号和内容。
type ConfigChangedNotify struct {
	Selector      Selector        `json:"selector"`
	Scope         string          `json:"scope,omitempty"`
	ConfigVersion int64           `json:"config_version"`
	Payload       json.RawMessage `json:"payload"`
}

// LSPReleaseScopeRequest 是 mcpcontrol 向 mcp-lsp 进程发送的管理回调载荷，
// 指示 mcp-lsp 释放指定 scope 下的 manager 和缓存。
type LSPReleaseScopeRequest struct {
	ScopeKind  string `json:"scope_kind"`
	AgentID    string `json:"agent_id,omitempty"`
	ThreadID   string `json:"thread_id,omitempty"`
	ManagerKey string `json:"manager_key,omitempty"`
	Drain      bool   `json:"drain,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// LSPReleaseScopeResult 报告 mcp-lsp 在 ReleaseScope 管理回调中完成的本地清理结果。
type LSPReleaseScopeResult struct {
	MatchedManagers int      `json:"matched_managers"`
	ClosedManagers  int      `json:"closed_managers"`
	BusyLeases      int      `json:"busy_leases"`
	Drained         bool     `json:"drained,omitempty"`
	ScopeKeys       []string `json:"scope_keys,omitempty"`
	ManagerKeys     []string `json:"manager_keys,omitempty"`
}
