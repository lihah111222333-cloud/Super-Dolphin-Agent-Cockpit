package mcpcontrol

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/kelindar/event"
)

// AgentContextSource provides agent snapshots used to build MCP context responses.
type AgentContextSource interface {
	GetAgentSnapshot(agentID string) (*contract.AgentSnapshot, error)
}

// ContextProvider resolves a context request for a registered MCP tool instance.
type ContextProvider interface {
	GetContext(ctx context.Context, instance *ToolInstance, req dto.ContextRequest) (dto.ContextResponse, error)
}

// EventSink handles event notifications emitted by a registered MCP tool instance.
type EventSink interface {
	HandleEvent(ctx context.Context, instance *ToolInstance, req dto.EventNotify) error
}

// LogSink handles log notifications emitted by a registered MCP tool instance.
type LogSink interface {
	HandleLog(ctx context.Context, instance *ToolInstance, req dto.LogNotify) error
}

type ackResponse struct {
	OK bool `json:"ok"`
}

type controlEvent struct {
	Lease      dto.LeaseKey    `json:"lease"`
	EventID    string          `json:"event_id,omitempty"`
	EventType  string          `json:"event_type"`
	AuditClass string          `json:"audit_class,omitempty"`
	Payload    json.RawMessage `json:"payload"`
}

const controlEventType uint32 = 0xC7100001

// Type 返回事件分发用的类型编号。
func (controlEvent) Type() uint32 { return controlEventType }

// NewHandlers 创建处理器。
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

// requestApproval 处理请求审批。
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
		payload := decodePayloadMap(req.Payload)
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

func approvalDecisionSource(decision contract.ApprovalDecision) string {
	switch strings.TrimSpace(decision.Reason) {
	case "auto_approved":
		return dto.DecisionSourceAutoApprove
	default:
		return dto.DecisionSourceUI
	}
}

type registryContextProvider struct {
	agents AgentContextSource
}

// GetContext 读取上下文。
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

type defaultEventSink struct {
	dispatcher *event.Dispatcher
	logger     *pkglogger.Logger
}

// HandleEvent 处理事件。
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

type defaultLogSink struct {
	logger *pkglogger.Logger
}

// HandleLog 处理日志。
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

// controlLogArgs 处理control日志args。
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
		args = append(args, key, req.Fields[key])
	}
	return args
}

func serverFromContext(ctx context.Context) (server *jrpc2.Server, err error) {
	server, _, err = resolveServerPeer(ctx)
	return server, err
}
