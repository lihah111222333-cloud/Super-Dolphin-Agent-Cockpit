package mcpcontrol

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/kelindar/event"
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
			agents: resolveAgentContextSource(p.AgentSource, p.Orchestration),
		}
	}
	eventSink := p.Events
	if eventSink == nil {
		eventSink = defaultEventSink{dispatcher: p.Dispatcher, logger: p.Logger}
	}
	logSink := p.Logs
	if logSink == nil {
		logSink = defaultLogSink{logger: p.Logger}
	}
	runtimeReports := p.RuntimeReports
	if runtimeReports == nil {
		runtimeReports = defaultRuntimeReportHandler{orchestration: p.Orchestration}
	}
	completionReports := p.CompletionReports
	if completionReports == nil {
		completionReports = defaultCompletionReportHandler{orchestration: p.Orchestration}
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

// resolveAgentContextSource 优先使用显式来源，否则从 orchestration 服务适配 AgentContextSource。
func resolveAgentContextSource(explicit AgentContextSource, orchestration contract.OrchestrationService) AgentContextSource {
	if explicit != nil {
		return explicit
	}
	if orchestration == nil {
		return nil
	}
	source, _ := orchestration.(AgentContextSource)
	return source
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

// defaultLogSink 将 MCP peer 日志转写到进程 logger。
type defaultLogSink struct {
	logger *pkglogger.Logger
}

// HandleLog 校验日志消息并追加 MCP 元数据字段；未配置 logger 时直接丢弃有效日志。
func (s defaultLogSink) HandleLog(ctx context.Context, instance *ToolInstance, req dto.LogNotify) error {
	message := strings.TrimSpace(req.Message)
	if message == "" {
		return errInvalidParams("mcp log message is required")
	}
	if s.logger == nil {
		return nil
	}
	s.logger.Log(ctx, controlLogLevel(req.Level), message, controlLogArgs(instance, req)...)
	return nil
}

// controlLogLevel 将 peer 字符串级别映射到 slog.Level，未知级别按 info 处理。
func controlLogLevel(level string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
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
