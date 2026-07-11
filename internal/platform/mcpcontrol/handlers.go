package mcpcontrol

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// AgentContextSource 提供 agent 快照，用于生成 MCP context 响应。
type AgentContextSource interface {
	GetAgentSnapshot(agentID string) (*contract.AgentSnapshot, error)
}

// ContextProvider 为已注册 MCP 工具实例解析 context 请求。
type ContextProvider interface {
	GetContext(ctx context.Context, instance *ToolInstance, req dto.ContextRequest) (dto.ContextResponse, error)
}

// EventSink 处理已注册 MCP 工具实例上报的事件通知。
type EventSink interface {
	HandleEvent(ctx context.Context, instance *ToolInstance, req dto.EventNotify) error
}

// LogSink 处理已注册 MCP 工具实例上报的日志通知。
type LogSink interface {
	HandleLog(ctx context.Context, instance *ToolInstance, req dto.LogNotify) error
}

// SystemLogSink 是 ctl/log 持久化所需的最小端口，由 app 层适配到 systemlog.Store。
type SystemLogSink interface {
	InsertSystemLog(ctx context.Context, entry SystemLogEntry) error
}

// SystemLogEntry 是 MCP peer 日志进入 system_logs 前的稳定 DTO。
type SystemLogEntry struct {
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

// ackResponse 是 event/log 类通知的统一确认响应。
type ackResponse struct {
	OK bool `json:"ok"`
}

// controlEvent 是 MCP peer 上报事件进入进程内事件总线的载荷。
type controlEvent struct {
	Lease      dto.LeaseKey    `json:"lease"`
	EventID    string          `json:"event_id,omitempty"`
	EventType  string          `json:"event_type"`
	AuditClass string          `json:"audit_class,omitempty"`
	Payload    json.RawMessage `json:"payload"`
}

const controlEventType uint32 = 0xC7100001

// Type 返回事件总线使用的稳定类型编号。
func (controlEvent) Type() uint32 { return controlEventType }

// NewHandlers 组装 MCP 控制面 RPC handler；缺省依赖会落到本包默认实现，缺核心依赖则在调用时 fail-fast。
func NewHandlers(p HandlerDeps) rpc.HandlerMapResult {
	contextProvider := p.Context
	if contextProvider == nil {
		contextProvider = registryContextProvider{
			agents: p.AgentSource,
		}
	}
	eventSink := p.Events
	if eventSink == nil {
		eventSink = defaultEventSink{dispatcher: p.Dispatcher, logger: p.Logger}
	}
	logSink := p.Logs
	if logSink == nil {
		logSink = defaultLogSink{logger: p.Logger, systemLogs: p.SystemLogs}
	}
	runtimeReports := p.RuntimeReports
	if runtimeReports == nil {
		runtimeReports = defaultRuntimeReportHandler{updates: p.RuntimeUpdates}
	}
	completionReports := p.CompletionReports
	if completionReports == nil {
		completionReports = defaultCompletionReportHandler{events: p.ReportEvents}
	}

	return rpc.HandlerMapResult{Handlers: handler.Map{
		dto.MethodRegister: rpc.StrictHandler(func(ctx context.Context, req dto.RegisterRequest) (dto.RegisterResponse, error) {
			return p.Registry.Register(ctx, req)
		}),
		dto.MethodHeartbeat: rpc.StrictHandler(func(ctx context.Context, req dto.HeartbeatRequest) (dto.HeartbeatResponse, error) {
			return p.Registry.Heartbeat(ctx, req)
		}),
		dto.MethodContext: rpc.StrictHandler(func(ctx context.Context, req dto.ContextRequest) (dto.ContextResponse, error) {
			return withResolvedInstance(p.Registry, req, func(req dto.ContextRequest) dto.LeaseKey {
				return dto.LeaseKey{InstanceID: req.InstanceID, Generation: req.Generation}
			}, func(instance *ToolInstance) (dto.ContextResponse, error) {
				return contextProvider.GetContext(ctx, instance, req)
			})
		}),
		dto.MethodEvent: rpc.StrictHandler(func(ctx context.Context, req dto.EventNotify) (ackResponse, error) {
			return withResolvedInstance(p.Registry, req, func(req dto.EventNotify) dto.LeaseKey {
				return dto.LeaseKey{InstanceID: req.InstanceID, Generation: req.Generation}
			}, func(instance *ToolInstance) (ackResponse, error) {
				return ackResponse{OK: true}, eventSink.HandleEvent(ctx, instance, req)
			})
		}),
		dto.MethodLog: rpc.StrictHandler(func(ctx context.Context, req dto.LogNotify) (ackResponse, error) {
			return withResolvedInstance(p.Registry, req, func(req dto.LogNotify) dto.LeaseKey {
				return dto.LeaseKey{InstanceID: req.InstanceID, Generation: req.Generation}
			}, func(instance *ToolInstance) (ackResponse, error) {
				return ackResponse{OK: true}, logSink.HandleLog(ctx, instance, req)
			})
		}),
		dto.MethodApproval: rpc.StrictHandler(func(ctx context.Context, req dto.ApprovalRequest) (dto.ApprovalResponse, error) {
			return requestApproval(ctx, p.Registry, p.Approvals, p.Bridge, req)
		}),
		dto.MethodHookSubscribe: rpc.StrictHandler(func(ctx context.Context, req dto.HookSubscribeRequest) (dto.HookSubscribeResponse, error) {
			return handleHookSubscribe(ctx, p.Registry, p.HookManager, req)
		}),
		dto.MethodHookResolve: rpc.StrictHandler(func(ctx context.Context, req dto.HookResolveRequest) (dto.HookResolveResponse, error) {
			return handleHookResolve(ctx, p.Registry, p.HookManager, req)
		}),
		dto.MethodHookPending: rpc.StrictHandler(func(ctx context.Context, req dto.HookPendingRequest) (dto.HookPendingResponse, error) {
			return handleHookPending(ctx, p.Registry, p.HookManager, req)
		}),
		dto.MethodReport: rpc.StrictHandler(func(ctx context.Context, req dto.ReportRequest) (dto.ReportResponse, error) {
			return handleReport(ctx, p.Registry, runtimeReports, completionReports, req)
		}),
	}}
}

// requestApproval 把 MCP approval 请求转发到 UI bridge，超时由请求参数或默认 peer 超时控制。
func requestApproval(
	ctx context.Context,
	registry *ToolRegistry,
	approvals *rpc.ApprovalManager,
	bridge *rpc.PushBridge,
	req dto.ApprovalRequest,
) (dto.ApprovalResponse, error) {
	if approvals == nil {
		return dto.ApprovalResponse{}, errApprovalUnavailable("mcp approval manager is not configured")
	}
	server, err := serverFromContext(ctx)
	if err != nil {
		return dto.ApprovalResponse{}, err
	}
	return withResolvedInstance(registry, req, func(req dto.ApprovalRequest) dto.LeaseKey {
		return dto.LeaseKey{InstanceID: req.InstanceID, Generation: req.Generation}
	}, func(instance *ToolInstance) (dto.ApprovalResponse, error) {
		callCtx := ctx
		if req.TimeoutMs > 0 {
			var cancel context.CancelFunc
			callCtx, cancel = platformconfig.WithPeerTimeout(ctx, timeDurationMillis(req.TimeoutMs))
			defer cancel()
		}
		payload := trustedApprovalPayload(req.Payload)
		decision, err := approvals.RequestApproval(callCtx, bridge, server, rpc.ApprovalRequest{
			CallID:       req.CallID,
			ApprovalID:   req.CallID,
			ToolName:     req.ToolName,
			AgentID:      instance.AgentID,
			ThreadID:     instance.ThreadID,
			Reason:       req.Reason,
			Kind:         req.Kind,
			SourceMethod: dto.MethodApproval,
			Payload:      payload,
		})
		if err != nil {
			return dto.ApprovalResponse{}, err
		}
		return dto.ApprovalResponse{
			Approved:       decision.Approved,
			Reason:         decision.Reason,
			Detail:         append(json.RawMessage(nil), decision.Detail...),
			DecisionSource: approvalDecisionSource(decision),
		}, nil
	})
}

func trustedApprovalPayload(raw json.RawMessage) map[string]any {
	payload := decodePayloadMap(raw)
	delete(payload, "approvalPolicy")
	delete(payload, "approval_policy")
	return payload
}

// approvalDecisionSource 将 approval 结果映射为协议字段，保留 auto approve 和 UI 决策来源。
func approvalDecisionSource(decision contract.ApprovalDecision) string {
	switch strings.TrimSpace(decision.Reason) {
	case "auto_approved":
		return dto.DecisionSourceAutoApprove
	default:
		return dto.DecisionSourceUI
	}
}

// registryContextProvider 从注册表实例和 orchestration 快照拼装 MCP context 响应。
type registryContextProvider struct {
	agents AgentContextSource
}

// GetContext 读取请求 scope 的上下文，并按 req.Keys 做白名单过滤。
func (p registryContextProvider) GetContext(_ context.Context, instance *ToolInstance, req dto.ContextRequest) (dto.ContextResponse, error) {
	snapshot, err := p.lookupAgentSnapshot(req.AgentID)
	if err != nil {
		return dto.ContextResponse{}, err
	}
	payload, err := contextPayload(req.Scope, instance, snapshot)
	if err != nil {
		return dto.ContextResponse{}, err
	}
	return buildContextResponse(req.Scope, platformshared.FilterKeys(payload, req.Keys))
}

// lookupAgentSnapshot 读取 agent 快照；请求了 agent_id 但来源不可用时直接返回参数错误。
func (p registryContextProvider) lookupAgentSnapshot(agentID string) (*contract.AgentSnapshot, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, nil
	}
	if p.agents == nil {
		return nil, errInvalidParams("agent not found")
	}
	snapshot, err := p.agents.GetAgentSnapshot(agentID)
	if err != nil || snapshot == nil {
		return nil, errInvalidParams("agent not found")
	}
	return snapshot, nil
}

// defaultEventSink 将 MCP event 写入事件总线并输出控制面日志。
type defaultEventSink struct {
	dispatcher *event.Dispatcher
	logger     *pkglogger.Logger
}

// HandleEvent 校验事件类型后发布 controlEvent；没有 dispatcher 时只记录日志不吞掉入参错误。
func (s defaultEventSink) HandleEvent(_ context.Context, instance *ToolInstance, req dto.EventNotify) error {
	if strings.TrimSpace(req.EventType) == "" {
		return errInvalidParams("mcp event_type is required")
	}
	if s.dispatcher != nil {
		event.Publish(s.dispatcher, controlEvent{
			Lease:      instance.Lease,
			EventID:    req.EventID,
			EventType:  req.EventType,
			AuditClass: req.AuditClass,
			Payload:    append(json.RawMessage(nil), req.Payload...),
		})
	}
	if s.logger != nil {
		s.logger.Info("mcp control event", "instance_id", instance.Lease.InstanceID, "generation", instance.Lease.Generation, "event_type", req.EventType)
	}
	return nil
}

// defaultLogSink 将 MCP peer 日志写入 system_logs，并可同步转写到进程 logger。
type defaultLogSink struct {
	logger     *pkglogger.Logger
	systemLogs SystemLogSink
}

// HandleLog 校验日志消息并追加 MCP 元数据字段，同时把脱敏后的日志持久化到 system_logs。
func (s defaultLogSink) HandleLog(ctx context.Context, instance *ToolInstance, req dto.LogNotify) error {
	message := strings.TrimSpace(req.Message)
	if message == "" {
		return errInvalidParams("mcp log message is required")
	}
	logLevel, slogLevel, err := parseControlLogLevel(req.Level)
	if err != nil {
		return err
	}
	var entry SystemLogEntry
	if s.systemLogs != nil {
		entry, err = controlSystemLogEntry(instance, req, message, logLevel)
		if err != nil {
			return err
		}
	}
	if s.logger != nil {
		s.logger.Log(ctx, slogLevel, message, controlLogArgs(instance, req)...)
	}
	if s.systemLogs == nil {
		return errInternal("mcp log sink is not configured")
	}
	if err := s.systemLogs.InsertSystemLog(ctx, entry); err != nil {
		return errInternal("mcp system log insert failed: %v", err)
	}
	return nil
}

// parseControlLogLevel 将 peer 字符串级别映射为 system_logs 和 slog 级别；未知或缺失级别直接阻断。
func parseControlLogLevel(level string) (string, slog.Level, error) {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		return "debug", slog.LevelDebug, nil
	case "INFO":
		return "info", slog.LevelInfo, nil
	case "WARN", "WARNING":
		return "warn", slog.LevelWarn, nil
	case "ERROR":
		return "error", slog.LevelError, nil
	default:
		return "", slog.LevelInfo, errInvalidParams("mcp log level must be one of debug, info, warn, error")
	}
}

// controlLogArgs 构造 MCP 日志的结构化字段，用户字段按 key 排序以保持输出稳定。
func controlLogArgs(instance *ToolInstance, req dto.LogNotify) []any {
	if instance == nil {
		instance = &ToolInstance{}
	}
	args := []any{
		"source", "mcp-control",
		"mcp_instance_id", instance.Lease.InstanceID,
		"mcp_generation", instance.Lease.Generation,
		"mcp_binary_name", instance.BinaryName,
		"mcp_client_kind", instance.ClientKind,
		"mcp_pid", instance.PID,
		"mcp_log_seq", req.Seq,
		"mcp_log_ts", req.TS,
	}
	if agentID := strings.TrimSpace(instance.AgentID); agentID != "" {
		args = append(args, "agent_id", agentID)
	}
	if threadID := strings.TrimSpace(instance.ThreadID); threadID != "" {
		args = append(args, "thread_id", threadID)
	}
	keys := make([]string, 0, len(req.Fields))
	for key := range req.Fields {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, controlSafeLogField(key, req.Fields[key])...)
	}
	return args
}

// controlSystemLogEntry 把 ctl/log 请求投影为 system_logs 行，敏感字段只放脱敏摘要。
func controlSystemLogEntry(instance *ToolInstance, req dto.LogNotify, message string, level string) (SystemLogEntry, error) {
	if instance == nil {
		instance = &ToolInstance{}
	}
	traceID, spanID, parentSpanID, err := controlTraceFields(req.Fields)
	if err != nil {
		return SystemLogEntry{}, err
	}
	durationMs, err := int32FieldFromAny(req.Fields["duration_ms"])
	if err != nil {
		return SystemLogEntry{}, err
	}
	extra, err := controlSystemLogExtra(instance, req)
	if err != nil {
		return SystemLogEntry{}, err
	}
	return SystemLogEntry{
		Level:        level,
		Logger:       "mcp-control",
		Message:      message,
		Raw:          string(extra),
		Source:       "mcp-control",
		Component:    firstControlLogString(instance.BinaryName, stringFieldFromAny(req.Fields["component"])),
		AgentID:      firstControlLogString(instance.AgentID, stringFieldFromAny(req.Fields["agent_id"])),
		ThreadID:     firstControlLogString(instance.ThreadID, stringFieldFromAny(req.Fields["thread_id"])),
		TraceID:      traceID,
		SpanID:       spanID,
		ParentSpanID: parentSpanID,
		EventType:    dto.MethodLog,
		ToolName:     firstControlLogString(stringFieldFromAny(req.Fields["tool_name"]), instance.BinaryName),
		DurationMs:   durationMs,
		Extra:        extra,
	}, nil
}

func controlSystemLogExtra(instance *ToolInstance, req dto.LogNotify) (json.RawMessage, error) {
	extra := map[string]any{
		"mcp_instance_id": instance.Lease.InstanceID,
		"mcp_generation":  instance.Lease.Generation,
		"mcp_binary_name": instance.BinaryName,
		"mcp_client_kind": instance.ClientKind,
		"mcp_pid":         instance.PID,
		"mcp_log_seq":     req.Seq,
		"mcp_log_ts":      req.TS,
		"fields":          controlSafeLogFields(req.Fields),
	}
	data, err := json.Marshal(extra)
	if err != nil {
		return nil, errInternal("marshal mcp system log extra: %v", err)
	}
	return data, nil
}

// controlSafeLogFields 复用 observability 脱敏规则，把 peer 字段压成可落库的 JSON 对象。
func controlSafeLogFields(fields map[string]any) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]any, len(fields))
	keys := make([]string, 0, len(fields))
	for key := range fields {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if safe, ok := observability.SafeMetadataValue(key, fields[key], 512); ok {
			out[key] = safe
			continue
		}
		preview := observability.SafePreview(fields[key], 512)
		out[key+"_preview"] = preview.Preview
		out[key+"_bytes"] = preview.Bytes
		out[key+"_truncated"] = preview.Truncated
		if preview.SHA256 != "" {
			out[key+"_sha256"] = preview.SHA256
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// controlTraceFields 解析 ctl/log 的 trace/span 载体；显式 traceparent 与字段不一致时 fail-fast。
func controlTraceFields(fields map[string]any) (string, string, string, error) {
	trace, err := pkglogger.ExtractTraceCarrierFields(fields, pkglogger.DefaultTraceFieldAliases())
	if err != nil {
		return "", "", "", errInvalidParams("mcp log %v", err)
	}
	return trace.TraceID, trace.SpanID, trace.ParentSpanID, nil
}

func firstControlLogString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func stringFieldFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

// int32FieldFromAny 从 JSON/RPC 常见数字类型中提取 duration_ms；非法或越界时阻断 ctl/log。
func int32FieldFromAny(value any) (*int32, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case int:
		return int32PtrIfInRange(int64(typed))
	case int64:
		return int32PtrIfInRange(typed)
	case int32:
		return int32PtrIfInRange(int64(typed))
	case float64:
		if typed != float64(int64(typed)) {
			return nil, errInvalidParams("mcp log duration_ms must be an integer")
		}
		return int32PtrIfInRange(int64(typed))
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return nil, errInvalidParams("mcp log duration_ms must be an integer")
		}
		return int32PtrIfInRange(parsed)
	default:
		return nil, errInvalidParams("mcp log duration_ms must be a number")
	}
}

func int32PtrIfInRange(value int64) (*int32, error) {
	if value < 0 || value > 1<<31-1 {
		return nil, errInvalidParams("mcp log duration_ms must fit int32 and be non-negative")
	}
	v := int32(value)
	return &v, nil
}

// controlSafeLogField 将 peer 自定义字段限制为脱敏、可 JSON 表达的日志字段。
func controlSafeLogField(key string, value any) []any {
	if safe, ok := observability.SafeMetadataValue(key, value, 512); ok {
		return []any{key, safe}
	}
	preview := observability.SafePreview(value, 512)
	args := []any{
		key + "_preview", preview.Preview,
		key + "_bytes", preview.Bytes,
		key + "_truncated", preview.Truncated,
	}
	if preview.SHA256 != "" {
		args = append(args, key+"_sha256", preview.SHA256)
	}
	return args
}

// serverFromContext 只返回当前 jrpc2 server，供需要 UI bridge server 的 handler 使用。
func serverFromContext(ctx context.Context) (server *jrpc2.Server, err error) {
	server, _, err = resolveServerPeer(ctx)
	return server, err
}
