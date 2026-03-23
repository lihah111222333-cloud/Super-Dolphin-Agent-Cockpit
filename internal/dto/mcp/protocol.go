package mcp

import "encoding/json"

// LeaseKey is the canonical identity for a registered lifecycle lease.
type LeaseKey struct {
	InstanceID string `json:"instance_id"`
	Generation uint64 `json:"generation"`
}

// RegisterRequest is the payload for ctl/register.
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
	CapabilitiesOffered  []string `json:"capabilities_offered,omitempty"`
	CapabilitiesRequired []string `json:"capabilities_required,omitempty"`
	Subscriptions        []string `json:"subscriptions,omitempty"`
	ResumeFromGeneration *uint64  `json:"resume_from_generation,omitempty"`
}

// RegisterResponse is the response for ctl/register.
type RegisterResponse struct {
	Lease                  LeaseKey `json:"lease"`
	// Deprecated: use LeaseKey. Will be removed after 2026-06-30.
	LeaseID                string   `json:"lease_id,omitempty"`
	AcceptedGeneration     uint64   `json:"accepted_generation,omitempty"`
	PeerKind               string   `json:"peer_kind,omitempty"`
	CapabilitiesNegotiated []string `json:"capabilities_negotiated,omitempty"`
	CapabilitiesRejected   []string `json:"capabilities_rejected,omitempty"`
	HeartbeatIntervalMs    int      `json:"heartbeat_interval_ms"`
	HeartbeatTimeoutMs     int      `json:"heartbeat_timeout_ms"`
	SendTimeoutMs          int      `json:"send_timeout_ms,omitempty"`
	SweeperIntervalMs      int      `json:"sweeper_interval_ms,omitempty"`
	ServerProtocolVersion  string   `json:"server_protocol_version,omitempty"`
	ConfigVersion          int64    `json:"config_version"`
}

// HeartbeatRequest is the payload for ctl/heartbeat.
type HeartbeatRequest struct {
	Lease                 LeaseKey        `json:"lease"`
	HeartbeatSeq          uint64          `json:"heartbeat_seq"`
	Status                string          `json:"status,omitempty"`
	Metrics               json.RawMessage `json:"metrics,omitempty"`
	ObservedConfigVersion int64           `json:"observed_config_version,omitempty"`
	InstanceID            string          `json:"instance_id,omitempty"`
	// Deprecated: use LeaseKey. Will be removed after 2026-06-30.
	LeaseID               string          `json:"lease_id,omitempty"`
}

// HeartbeatResponse is the response for ctl/heartbeat.
type HeartbeatResponse struct {
	OK              bool  `json:"ok"`
	ServerTime      int64 `json:"server_time"`
	ConfigVersion   int64 `json:"config_version"`
	NextHeartbeatMs int   `json:"next_heartbeat_ms,omitempty"`
}

// ContextRequest is the payload for ctl/context.
type ContextRequest struct {
	Lease LeaseKey `json:"lease"`
	// Deprecated compatibility mirror.
	InstanceID string   `json:"instance_id,omitempty"`
	AgentID    string   `json:"agent_id,omitempty"`
	Scope      string   `json:"scope"` // agent.runtime / thread.binding / workspace.run / config.snapshot
	Keys       []string `json:"keys,omitempty"`
}

// ContextResponse is the response for ctl/context.
type ContextResponse struct {
	Source     string          `json:"source"` // live / boot_snapshot / db_rebuild
	ObservedAt int64           `json:"observed_at"`
	Scope      string          `json:"scope,omitempty"`
	Payload    json.RawMessage `json:"payload"`
	Data       json.RawMessage `json:"data,omitempty"`
}

// EventNotify is the notify payload for ctl/event.
type EventNotify struct {
	Lease LeaseKey `json:"lease"`
	// Deprecated compatibility mirror.
	InstanceID string          `json:"instance_id,omitempty"`
	EventID    string          `json:"event_id,omitempty"`
	EventType  string          `json:"event_type"`
	AuditClass string          `json:"audit_class,omitempty"`
	Payload    json.RawMessage `json:"payload"`
}

// LogNotify is the notify payload for ctl/log.
type LogNotify struct {
	Lease LeaseKey `json:"lease"`
	// Deprecated compatibility mirror.
	InstanceID string         `json:"instance_id,omitempty"`
	Seq        uint64         `json:"seq,omitempty"`
	Level      string         `json:"level"`
	Message    string         `json:"message"`
	Fields     map[string]any `json:"fields,omitempty"`
	TS         int64          `json:"ts,omitempty"`
}

// ApprovalRequest is the request payload for ctl/approval/request.
type ApprovalRequest struct {
	Lease LeaseKey `json:"lease"`
	// Deprecated compatibility mirrors.
	InstanceID string          `json:"instance_id,omitempty"`
	// Deprecated: use LeaseKey. Will be removed after 2026-06-30.
	LeaseID    string          `json:"lease_id,omitempty"`
	CallID     string          `json:"call_id"`
	ToolName   string          `json:"tool_name"`
	Reason     string          `json:"reason"`
	Kind       string          `json:"kind"`
	Payload    json.RawMessage `json:"payload"`
	TimeoutMs  int             `json:"timeout_ms,omitempty"`
}

// RuntimeReport is the durable runtime variant of ctl/report.
type RuntimeReport struct {
	Port     int    `json:"port,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// CompletionReport is the durable completion variant of ctl/report.
type CompletionReport struct {
	Status   string          `json:"status"`
	Report   string          `json:"report,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// ProgressReport is a reserved ctl/report variant.
type ProgressReport struct {
	Status   string   `json:"status,omitempty"`
	Message  string   `json:"message,omitempty"`
	Percent  *float64 `json:"percent,omitempty"`
	Step     string   `json:"step,omitempty"`
	Sequence uint64   `json:"sequence,omitempty"`
}

// DiagnosticReport is a reserved ctl/report variant.
type DiagnosticReport struct {
	Level   string          `json:"level,omitempty"`
	Code    string          `json:"code,omitempty"`
	Message string          `json:"message,omitempty"`
	Details json.RawMessage `json:"details,omitempty"`
}

// ReportEnvelope is the discriminated union payload for ctl/report.
type ReportEnvelope struct {
	Type       string            `json:"type"`
	Runtime    *RuntimeReport    `json:"runtime,omitempty"`
	Completion *CompletionReport `json:"completion,omitempty"`
	Progress   *ProgressReport   `json:"progress,omitempty"`
	Diagnostic *DiagnosticReport `json:"diagnostic,omitempty"`
}

// ReportRequest is the request payload for ctl/report.
type ReportRequest struct {
	Lease LeaseKey `json:"lease"`
	// Deprecated compatibility mirrors.
	InstanceID string         `json:"instance_id,omitempty"`
	// Deprecated: use LeaseKey. Will be removed after 2026-06-30.
	LeaseID    string         `json:"lease_id,omitempty"`
	ReportID   string         `json:"report_id"` // idempotency key
	Report     ReportEnvelope `json:"report"`
}

// ReportResponse is the response for ctl/report.
type ReportResponse struct {
	Accepted        bool   `json:"accepted"`
	Success         bool   `json:"success,omitempty"`
	PersistedAt     int64  `json:"persisted_at,omitempty"`
	CanonicalStatus string `json:"canonical_status,omitempty"`
	AppliedVariant  string `json:"applied_variant,omitempty"`
}

// ShutdownRequest is the request payload for ctl/shutdown.
type ShutdownRequest struct {
	Lease               LeaseKey `json:"lease"`
	ShutdownID          string   `json:"shutdown_id"`
	Reason              string   `json:"reason"`
	TimeoutMs           int      `json:"timeout_ms,omitempty"`
	DeadlineMs          int      `json:"deadline_ms,omitempty"`
	FinalReportExpected bool     `json:"final_report_expected,omitempty"`
}

// SelectorScope is the target scope inside a config selector.
type SelectorScope struct {
	AgentID    string `json:"agent_id,omitempty"`
	ThreadID   string `json:"thread_id,omitempty"`
	ClientKind string `json:"client_kind,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
}

// Selector filters config fanout by intersection(subscription, capability, scope).
type Selector struct {
	Subscription string         `json:"subscription,omitempty"`
	Capability   string         `json:"capability,omitempty"`
	Scope        *SelectorScope `json:"scope,omitempty"`
}

// ConfigChangedNotify is the notify payload for ctl/config/changed.
type ConfigChangedNotify struct {
	Selector      Selector        `json:"selector"`
	Scope         string          `json:"scope,omitempty"`
	ConfigVersion int64           `json:"config_version"`
	Payload       json.RawMessage `json:"payload"`
}
